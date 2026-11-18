"""Internal bounded text transform. Go owns reads, writes, consent and recovery."""
import copy
import difflib
import json
import sys
import tomllib

# -I -S prevents site customization and project imports. The sole extra module
# location is the verified private wheel location supplied by the Go caller.
sys.path.insert(0, sys.argv[1])
import tomlkit
from tomlkit.exceptions import TOMLKitError
from tomlkit.items import InlineTable, Table


def refuse(reason):
    """Report the document being merged as one whose layout cannot take the
    policy without changing what it means, and stop; nothing is written."""
    json.dump({"refused": index, "reason": reason}, sys.stdout)
    sys.exit(0)


def layout(doc, path):
    """Return how the tables on path are written: "inline" when one is an
    inline table, "dotted" when one is defined by dotted keys (a.b = 1), or ""
    when all are [table] headers. Every fragment of a table is looked at,
    because out-of-order headers and dotted keys split one table into several."""
    containers, found = [doc], ""
    for name in path:
        entries = [
            (key, item)
            for container in containers
            for key, item in container.body
            if key is not None and key.key == name
        ]
        for key, item in entries:
            if isinstance(item, InlineTable):
                return "inline"
            if key.is_dotted():
                found = "dotted"
        containers = [item.value for _, item in entries if isinstance(item, Table)]
    return found


def refuse_inline(doc, path, key):
    for depth in range(1, len(path) + 1):
        if layout(doc, path[:depth]) == "inline":
            name = ".".join(path[:depth])
            refuse(
                f"{name} is an inline table, so {'.'.join(path + [key])} cannot be "
                f"added to it; write it as a [{name}] table"
            )


def merge_missing(doc, target, policy, path, headers):
    for key, value in policy.items():
        if key not in target:
            refuse_inline(doc, path, key)
            # A table added inside dotted keys would render as a [header] among
            # them and take the keys after it, so it gets a [header] of its own
            # at the end of the document instead.
            if isinstance(value, dict) and path and layout(doc, path) == "dotted":
                headers.append((path + [key], value))
            else:
                target[key] = value
        elif isinstance(value, dict):
            if not isinstance(target[key], dict):
                raise ValueError("incompatible policy table")
            merge_missing(doc, target[key], value, path + [key], headers)
        # Existing scalars and arrays are project policy; never overwrite or
        # extend an existing rule selection implicitly.


def append_header(doc, path, value):
    """Add value as the table at path, written as a [path] header: at the end
    of the nearest table on path written as a [header], or of the document when
    the dotted keys start at its root. The tables between are implicit and
    define nothing."""
    depth = max(n for n in range(len(path)) if layout(doc, path[:n]) == "")
    anchor = lookup(doc, path[:depth])
    if depth and not (isinstance(anchor, Table) and not anchor.is_super_table()):
        anchor, depth = doc, 0
    item = value
    for key in reversed(path[depth + 1:]):
        parent = tomlkit.table(is_super_table=True)
        parent.append(key, item)
        item = parent
    try:
        anchor.append(path[depth], item)
    except TOMLKitError:
        refuse(f"a [{'.'.join(path)}] table cannot be added beside the dotted keys before it")
    # A blank line keeps the new table apart from a header right after it.
    if not value.is_super_table():
        header = f"[{'.'.join(path)}]\n{tomlkit.dumps(value)}"
        rendered = tomlkit.dumps(doc)
        at = rendered.find(header)
        if at >= 0 and rendered[at + len(header):].startswith("["):
            value.add(tomlkit.nl())


def lines(text):
    """Split text after each line feed, keeping it, so CRLF and LF lines stay
    distinct."""
    parts = text.split("\n")
    return [part + "\n" for part in parts[:-1]] + ([parts[-1]] if parts[-1] else [])


def own_line_endings(source, rendered):
    """Give each line the merge added the source's line ending, the one its
    first line ends with; TOML Kit writes them with LF. Existing lines keep
    their own, also in a file whose endings are mixed."""
    end = source.find("\n")
    if end <= 0 or source[end - 1] != "\r":
        return rendered
    old, new = lines(source), lines(rendered)
    result = []
    matcher = difflib.SequenceMatcher(None, old, new, autojunk=False)
    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        for n, line in enumerate(new[j1:j2]):
            if tag != "equal" and line.endswith("\n") and not line.endswith("\r\n"):
                # An existing line TOML Kit changed, such as a record's
                # policies list, keeps the ending it had.
                paired = old[i1 + n] if tag == "replace" and i1 + n < i2 else ""
                ending = "\n" if paired.endswith("\n") and not paired.endswith("\r\n") else "\r\n"
                line = line[:-1] + ending
            result.append(line)
    return "".join(result)


def plain_merge(target, policy):
    for key, value in policy.items():
        if key not in target:
            target[key] = copy.deepcopy(value)
        elif isinstance(value, dict) and isinstance(target[key], dict):
            plain_merge(target[key], value)


def same(a, b):
    """Strict equality of parsed TOML values: types must match (True is not 1)
    and floats compare by their text, so nan and -0.0 compare like any value."""
    if type(a) is not type(b):
        return False
    if isinstance(a, dict):
        return a.keys() == b.keys() and all(same(a[key], b[key]) for key in a)
    if isinstance(a, list):
        return len(a) == len(b) and all(map(same, a, b))
    if isinstance(a, float):
        return repr(a) == repr(b)
    return a == b


def lookup(doc, path):
    for key in path:
        if not isinstance(doc, dict) or key not in doc:
            return None
        doc = doc[key]
    return doc


# Each document names its policy and where its Workbench record lives: a key
# path such as tool.workbench, workspace.metadata.workbench or
# package.metadata.workbench, or none for a document that holds no record.
# The response is the rendered documents in order, or the index of the first
# document refused and why.
request = json.load(sys.stdin)
if tomlkit.__version__ != request["version"]:
    raise ValueError("unqualified TOML Kit version")
result = []
for index, document in enumerate(request["documents"]):
    source = document["source"]
    doc = tomlkit.parse(source)
    if tomlkit.dumps(doc) != source:
        raise ValueError("input needs normalization; propose a manual edit")
    record_path = document["provenance"]
    if record_path:
        previous = lookup(doc, record_path)
        if previous is not None and (
            not isinstance(previous, dict)
            or type(previous.get("schema_version")) is not tomlkit.items.Integer
            or previous["schema_version"] != 1
        ):
            raise ValueError("unsupported Workbench provenance")
    proposed_policy = tomlkit.parse(document["policy"])
    # The Ruff rule selection is Python's: an existing selection is the
    # project's own and the policy's is left out.
    if document["ruff"]:
        existing_ruff = doc.get("tool", {}).get("ruff", {})
        existing_lint = existing_ruff.get("lint", {})
        selection_keys = (
            "select", "ignore", "extend-select", "extend-ignore",
            "per-file-ignores", "extend-per-file-ignores",
        )
        if any(key in owner for owner in (existing_ruff, existing_lint) for key in selection_keys):
            del proposed_policy["tool"]["ruff"]["lint"]
    # What the result must mean: every value of the source, unchanged at its
    # own key, and exactly what the policy and the record add.
    try:
        expected = tomllib.loads(source)
    except tomllib.TOMLDecodeError:
        refuse(
            "it uses syntax newer than TOML 1.0, such as an inline table written "
            "over several lines, which the check of the merged result cannot read; "
            "write it in TOML 1.0"
        )
    plain_merge(expected, tomllib.loads(tomlkit.dumps(proposed_policy)))
    headers = []
    merge_missing(doc, doc, proposed_policy, [], headers)
    if record_path:
        expected_record = expected
        for key in record_path:
            expected_record = expected_record.setdefault(key, {})
        expected_record["schema_version"] = 1
        if document["policy_id"] not in expected_record.setdefault("policies", []):
            expected_record["policies"].append(document["policy_id"])
        created = lookup(doc, record_path) is None
        if created:
            refuse_inline(doc, record_path[:-1], record_path[-1])
        header = created and layout(doc, record_path[:-1]) == "dotted"
        if header:
            provenance = tomlkit.table()
            headers.append((record_path, provenance))
        else:
            owner = doc
            for key in record_path[:-1]:
                owner = owner.setdefault(key, tomlkit.table(is_super_table=True))
                if not isinstance(owner, dict):
                    raise ValueError("invalid policy provenance")
            provenance = owner.setdefault(record_path[-1], tomlkit.table())
        provenance["schema_version"] = 1
        policies = provenance.setdefault("policies", [])
        if not isinstance(policies, list):
            raise ValueError("invalid policy provenance")
        if document["policy_id"] not in policies:
            policies.append(document["policy_id"])
        # A record created inside a Cargo table lands before the tables that
        # follow it; a blank line keeps it apart from a header right after it.
        if created and not header and record_path[0] != "tool":
            text, rendered = tomlkit.dumps(provenance), tomlkit.dumps(doc)
            if rendered[rendered.find(text) + len(text):].startswith("["):
                provenance.add(tomlkit.nl())
    for path, value in headers:
        append_header(doc, path, value)
    rendered = own_line_endings(source, tomlkit.dumps(doc))
    try:
        reparsed = tomlkit.dumps(tomlkit.parse(rendered))
    except TOMLKitError:
        if not headers:
            raise
        names = ", ".join(f"[{'.'.join(path)}]" for path, _ in headers)
        refuse(f"TOML Kit cannot read back {names} added beside the dotted keys before it")
    if reparsed != rendered:
        raise ValueError("proposed document does not roundtrip")
    if not same(tomllib.loads(rendered), expected):
        refuse("the merged text would change the meaning of keys the file already has")
    result.append(rendered)
json.dump(result, sys.stdout)
