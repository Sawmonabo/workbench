"""Internal bounded text transform. Go owns reads, writes, consent and recovery."""
import json
import sys

# -I -S prevents site customization and project imports. The sole extra module
# location is the verified private wheel location supplied by the Go caller.
sys.path.insert(0, sys.argv[1])
import tomlkit


def merge_missing(target, policy):
    for key, value in policy.items():
        if key not in target:
            target[key] = value
        elif isinstance(value, dict):
            if not isinstance(target[key], dict):
                raise ValueError("incompatible policy table")
            merge_missing(target[key], value)
        # Existing scalars and arrays are project policy; never overwrite or
        # extend an existing rule selection implicitly.


def lookup(doc, path):
    for key in path:
        if not isinstance(doc, dict) or key not in doc:
            return None
        doc = doc[key]
    return doc


# Each document names its policy and where its Workbench record lives: a key
# path such as tool.workbench, workspace.metadata.workbench or
# package.metadata.workbench, or none for a document that holds no record.
request = json.load(sys.stdin)
if tomlkit.__version__ != request["version"]:
    raise ValueError("unqualified TOML Kit version")
result = []
for document in request["documents"]:
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
    merge_missing(doc, proposed_policy)
    if record_path:
        owner = doc
        for key in record_path[:-1]:
            owner = owner.setdefault(key, tomlkit.table(is_super_table=True))
            if not isinstance(owner, dict):
                raise ValueError("invalid policy provenance")
        created = record_path[-1] not in owner
        provenance = owner.setdefault(record_path[-1], tomlkit.table())
        provenance["schema_version"] = 1
        policies = provenance.setdefault("policies", [])
        if not isinstance(policies, list):
            raise ValueError("invalid policy provenance")
        if document["policy_id"] not in policies:
            policies.append(document["policy_id"])
        # A record created inside a Cargo table lands before the tables that
        # follow it; a blank line keeps it apart from a header right after it.
        if created and record_path[0] != "tool":
            text, rendered = tomlkit.dumps(provenance), tomlkit.dumps(doc)
            if rendered[rendered.find(text) + len(text):].startswith("["):
                provenance.add(tomlkit.nl())
    rendered = tomlkit.dumps(doc)
    if tomlkit.dumps(tomlkit.parse(rendered)) != rendered:
        raise ValueError("proposed document does not roundtrip")
    result.append(rendered)
json.dump(result, sys.stdout)
