#!/usr/bin/env python3
"""Refuse any workflow action pin that differs from versions.toml [github_actions]."""
from pathlib import Path
import re
import tomllib

root = Path(__file__).resolve().parent.parent
pins = tomllib.loads((root / "home/.chezmoidata/versions.toml").read_text())["github_actions"]
for name, pin in pins.items():
    if not re.fullmatch(r"[0-9a-f]{40}", pin["commit"]):
        raise SystemExit(f"{name} in versions.toml must be pinned by a 40-hex commit")
workflows = sorted((root / ".github/workflows").glob("*.y*ml")) + [root / "project/python/checks.example.yml"]
for workflow in workflows:
    for number, line in enumerate(workflow.read_text().splitlines(), 1):
        used = re.search(r"uses:\s*(\S+)(?:\s+#\s*(\S+))?", line)
        if used is None or used.group(1).startswith("./"):
            continue
        name, _, commit = used.group(1).partition("@")
        pin = pins.get(name)
        if pin is None or (commit, used.group(2)) != (pin["commit"], pin["version"]):
            raise SystemExit(f"{workflow.relative_to(root)}:{number}: {name} differs from versions.toml [github_actions]")
