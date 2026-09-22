#!/usr/bin/env python3
"""Build one Workbench release bundle for a target: CLI, machine sources and project policy."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--version", required=True)
parser.add_argument("--target", required=True, choices=["darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"])
parser.add_argument("--output", required=True, type=Path)
args = parser.parse_args()
if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", args.version):
    parser.error("version must be a release identifier")
subprocess.run(["python3", "scripts/generate-source-trust.py", "--check"], cwd=root, check=True)
payload = {}
for base in [root / ".chezmoiroot", root / "home", root / "project"]:
    if not base.exists():
        raise SystemExit(f"Missing required payload: {base.name}")
    for item in sorted(base.rglob("*")) if base.is_dir() else [base]:
        if item.is_symlink():
            raise SystemExit(f"Payload links are forbidden: {item}")
        if item.is_file():
            relative = item.relative_to(root)
            if relative.parts[0] == "project" and relative.as_posix() not in {
                "project/python/policy.toml", "project/python/gitignore.entries",
                "project/python/extensions.json", "project/python/checks.example.yml",
            }:
                continue
            if any(part in {".git", "__pycache__", ".venv", ".env", ".DS_Store"} for part in relative.parts):
                raise SystemExit(f"Unexpected private/generated payload: {relative}")
            payload[str(relative)] = item.read_bytes()
payload["licenses/NOTICE"] = (
    "Workbench personal release. No redistribution license has been granted.\n"
    "Third-party notices are included below; they do not grant rights to Workbench sources.\n"
).encode()
# Include exact licenses of linked modules, plus the Go runtime. Never copy host
# source trees wholesale into artifacts. Listing packages first downloads the
# linked modules, so the module listing below has their directories.
linked = set(subprocess.check_output(["go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./cmd/workbench"], cwd=root, text=True).splitlines())
modules = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=root, text=True)
decoder = json.JSONDecoder()
while modules.strip():
    module, end = decoder.raw_decode(modules.lstrip())
    modules = modules.lstrip()[end:]
    if module.get("Main") or module["Path"] not in linked:
        continue
    directory = Path(module["Dir"])
    licenses = [p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE", "COPYING", "NOTICE"))]
    if not licenses:
        raise SystemExit(f"License evidence missing for {module['Path']}")
    for license_file in licenses:
        payload[f"licenses/{module['Path'].replace('/', '_')}@{module['Version']}/{license_file.name}"] = license_file.read_bytes()
goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], cwd=root, text=True).strip())
go_license = next((p for p in [goroot / "LICENSE", goroot.parent / "LICENSE"] if p.is_file()), None)
if go_license is None:
    raise SystemExit("Go runtime license evidence is missing")
payload["licenses/go/LICENSE"] = go_license.read_bytes()
with tempfile.TemporaryDirectory(prefix="workbench-package-") as temporary:
    executable = Path(temporary) / "workbench"
    goos, goarch = args.target.split("-")
    environment = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
    subprocess.run(["go", "build", "-trimpath", "-ldflags", f"-X github.com/Sawmonabo/workbench/internal/cli.buildReleaseVersion={args.version}", "-o", str(executable), "./cmd/workbench"], cwd=root, env=environment, check=True)
    payload["bin/workbench"] = executable.read_bytes()
requirements = (root / "internal/machine/source-trust.json").read_bytes()
metadata = {
    "schema_version": 1, "state_version": 1, "release": args.version,
    "target": args.target, "source_digest": hashlib.sha256(requirements).hexdigest(),
    "files": {name: {"sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "executable": name == "bin/workbench"} for name, data in sorted(payload.items())},
    "requirements": json.loads(requirements),
}
payload["release.json"] = (json.dumps(metadata, sort_keys=True, separators=(",", ":")) + "\n").encode()
args.output.mkdir(parents=True, exist_ok=True)
bundle = args.output / f"workbench-{args.version}-{args.target}.tar.gz"
if bundle.exists():
    raise SystemExit("Refusing to overwrite an existing immutable bundle")
with tarfile.open(bundle, "w:gz", format=tarfile.USTAR_FORMAT) as archive:
    for name, data in sorted(payload.items()):
        info = tarfile.TarInfo(name)
        info.size, info.mode, info.mtime = len(data), 0o700 if name == "bin/workbench" else 0o600, 0
        archive.addfile(info, io.BytesIO(data))
print(bundle)
