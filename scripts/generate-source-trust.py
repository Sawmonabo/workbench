#!/usr/bin/env python3
"""Generate the executable's reviewed machine-source identity; never run sources."""
import hashlib
import json
from pathlib import Path
import sys
import tomllib

root = Path(__file__).resolve().parent.parent
paths = [root / ".chezmoiroot"] + sorted((root / "home").rglob("*"))
files = {}
for path in paths:
    if path.is_symlink():
        raise SystemExit("Machine source must contain only regular files/directories")
    if path.is_file():
        files[str(path.relative_to(root))] = hashlib.sha256(path.read_bytes()).hexdigest()
pins = tomllib.loads((root / "home/.chezmoidata/versions.toml").read_text())
manifest = {
    "files": files,
    "chezmoi": pins["management"]["chezmoi"],
    "uv": pins["versions"]["uv"],
    "uv_additional": pins["management"]["uv_additional"],
    "python": pins["versions"]["python_pinned"][0],
    "python_min_minor": pins["management"]["python_min_minor"],
    "python_max_minor": pins["management"]["python_max_minor"],
    "tomlkit": pins["management"]["tomlkit"],
    "tomlkit_sha256": pins["management"]["tomlkit_sha256"],
    "tomlkit_url": pins["management"]["tomlkit_url"],
    "tomlkit_license": pins["management"]["tomlkit_license"],
    "chezmoi_sha256": pins["management"]["chezmoi_sha256"],
    "uv_sha256": pins["management"]["uv_sha256"],
}
encoded = json.dumps(manifest, indent=2, sort_keys=True) + "\n"
target = root / "internal/machine/source-trust.json"
if "--check" in sys.argv:
    if not target.exists() or target.read_text() != encoded:
        raise SystemExit("Machine source trust is stale; review sources, then go generate ./internal/machine")
else:
    target.write_text(encoded)
