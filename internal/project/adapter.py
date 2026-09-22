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


request = json.load(sys.stdin)
if tomlkit.__version__ != request["version"]:
    raise ValueError("unqualified TOML Kit version")
result = []
for source in request["documents"]:
    doc = tomlkit.parse(source)
    if tomlkit.dumps(doc) != source:
        raise ValueError("input needs normalization; propose a manual edit")
    previous = doc.get("tool", {}).get("workbench")
    if previous is not None and (
        type(previous.get("schema_version")) is not tomlkit.items.Integer
        or previous["schema_version"] != 1
    ):
        raise ValueError("unsupported Workbench provenance")
    proposed_policy = tomlkit.parse(request["policy"])
    existing_ruff = doc.get("tool", {}).get("ruff", {})
    existing_lint = existing_ruff.get("lint", {})
    selection_keys = (
        "select", "ignore", "extend-select", "extend-ignore",
        "per-file-ignores", "extend-per-file-ignores",
    )
    if any(key in owner for owner in (existing_ruff, existing_lint) for key in selection_keys):
        del proposed_policy["tool"]["ruff"]["lint"]
    merge_missing(doc, proposed_policy)
    tool = doc.setdefault("tool", tomlkit.table())
    provenance = tool.setdefault("workbench", tomlkit.table())
    provenance["schema_version"] = 1
    provenance["release"] = request["release"]
    policies = provenance.setdefault("policies", [])
    if not isinstance(policies, list):
        raise ValueError("invalid policy provenance")
    if "python-v1" not in policies:
        policies.append("python-v1")
    rendered = tomlkit.dumps(doc)
    if tomlkit.dumps(tomlkit.parse(rendered)) != rendered:
        raise ValueError("proposed document does not roundtrip")
    result.append(rendered)
json.dump(result, sys.stdout)
