#!/usr/bin/env python3
"""Check the runtime pins in versions.toml against public support data.

Reads home/.chezmoidata/versions.toml and the endoflife.date data for Node,
Go, Python and Rust, and prints one line per stale pin. Rules, judged on
today's date:

- Node: every line in versions.node must not be past its end of life, and
  node_default must be on the newest line that is LTS today.
- Go: versions.go must be on a line that is not past its end of life.
- Python: every line in python_minors and python_pinned must not be past its
  end of life, and the newest released minor must be in python_minors.
- Rust: endoflife.date ends each line when the next one ships, so its dates
  are not used; the pin fails when it is more than four minor releases behind
  the latest stable.

Exit 0: every pin is current. Exit 1: a pin is stale. Exit 2: the support
data could not be fetched or read, so nothing was judged.

The data comes from https://endoflife.date/api/<product>.json, or, when
--data DIR or VERSION_PINS_DATA names a folder, from <product>.json files in
it (nodejs.json, go.json, python.json, rust.json), to run offline.
"""
import argparse
import datetime
import json
import os
import sys
import tomllib
import urllib.request
from pathlib import Path

API = "https://endoflife.date/api/{}.json"
RUST_MINORS_BEHIND = 4


class DataError(Exception):
    """The support data could not be fetched or read."""


def load(product, folder):
    try:
        if folder:
            text = (Path(folder) / f"{product}.json").read_text()
        else:
            request = urllib.request.Request(API.format(product), headers={"User-Agent": "version-pin-check"})
            with urllib.request.urlopen(request, timeout=30) as response:
                text = response.read().decode()
        cycles = json.loads(text)
    except (OSError, ValueError) as error:
        raise DataError(f"{product}: cannot read support data: {error}") from error
    if not isinstance(cycles, list) or not cycles or not all(isinstance(c, dict) and "cycle" in c for c in cycles):
        raise DataError(f"{product}: support data is not a list of release cycles")
    return {str(c["cycle"]): c for c in cycles}


def day(value, product, field):
    try:
        return datetime.date.fromisoformat(value)
    except (TypeError, ValueError) as error:
        raise DataError(f"{product}: {field} {value!r} is not a date") from error


def spoken(date):
    return f"{date:%b} {date.day}, {date.year}"


def ended(cycle, product, today):
    """Return the end-of-life date when the cycle is past it, else None."""
    eol = cycle.get("eol")
    if eol is False or eol is None:
        return None
    if eol is True:
        return today
    date = day(eol, product, "eol")
    return date if date <= today else None


def released(cycles, product, today):
    return [c for c in cycles.values() if day(c.get("releaseDate"), product, "releaseDate") <= today]


def minor_key(cycle):
    try:
        return tuple(int(part) for part in str(cycle["cycle"]).split("."))
    except ValueError as error:
        raise DataError(f"release cycle {cycle['cycle']!r} is not a version number") from error


def line(version, product, cycles):
    """Return the release cycle a pinned version belongs to."""
    parts = version.removeprefix("v").split(".")
    for name in (".".join(parts[:2]), parts[0]):
        if name in cycles:
            return cycles[name]
    raise DataError(f"{product}: no release cycle for pinned version {version}")


def check(pins, data, today):
    problems = []

    def eol_problem(label, version, product):
        date = ended(line(version, product, data[product]), product, today)
        if date is not None:
            problems.append(f"{label} {version}: end of life {spoken(date)}")

    node = data["nodejs"]
    for version in pins["node"]:
        eol_problem("Node", version, "nodejs")
    lts = [c for c in released(node, "nodejs", today)
           if c.get("lts") is True or (isinstance(c.get("lts"), str) and day(c["lts"], "nodejs", "lts") <= today)]
    if not lts:
        raise DataError("nodejs: no release cycle is LTS today")
    newest_lts = max(lts, key=minor_key)
    if line(pins["node_default"], "nodejs", node) is not newest_lts:
        problems.append(f"Node default {pins['node_default']}: not on the newest LTS line {newest_lts['cycle']}")

    eol_problem("Go", pins["go"], "go")

    for version in pins["python_minors"] + pins["python_pinned"]:
        eol_problem("Python", version, "python")
    newest_python = max(released(data["python"], "python", today), key=minor_key)
    if newest_python["cycle"] not in pins["python_minors"]:
        problems.append(f"Python {newest_python['cycle']}: newest released minor is missing from python_minors")

    rust = pins["rust"]
    latest_rust = max(released(data["rust"], "rust", today), key=minor_key)
    latest_major, latest_minor = minor_key(latest_rust)
    pin_major, pin_minor = minor_key(line(rust, "rust", data["rust"]))
    behind = latest_minor - pin_minor
    if latest_major != pin_major or behind > RUST_MINORS_BEHIND:
        problems.append(f"Rust {rust}: {behind} minor releases behind the latest stable {latest_rust['latest']}")
    return problems


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--data", default=os.environ.get("VERSION_PINS_DATA"),
                        help="folder of saved <product>.json files to read instead of endoflife.date")
    folder = parser.parse_args().data
    root = Path(__file__).resolve().parent.parent
    try:
        pins = tomllib.loads((root / "home/.chezmoidata/versions.toml").read_text())["versions"]
        data = {product: load(product, folder) for product in ("nodejs", "go", "python", "rust")}
        problems = check(pins, data, datetime.date.today())
    except (DataError, OSError, tomllib.TOMLDecodeError) as error:
        print(f"version pin check could not run: {error}", file=sys.stderr)
        return 2
    except Exception as error:  # noqa: BLE001 - any other surprise in the data or pins must not exit 1
        print(f"version pin check could not run: unexpected {type(error).__name__}: {error}", file=sys.stderr)
        return 2
    for problem in problems:
        print(problem)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
