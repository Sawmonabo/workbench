# Narrow settings merge: set one top-level key of a VS Code settings.json and
# change nothing else in the file.
#
#   python3 - FILE KEY VALUE           set KEY to VALUE
#   python3 - --check FILE KEY VALUE   only report whether that would change anything
#
# The file may be JSONC (comments and trailing commas), as VS Code allows. It
# is left untouched when KEY already has VALUE. Otherwise only the value is
# edited in place, or one member is added before the closing brace, so every
# other byte, comment included, stays as it is. When the file cannot be read
# safely, or the edit cannot be proven to change nothing else, nothing is
# written: duplicate keys, invalid JSON and a document that is not an object
# are refused, and so is an edit whose result does not parse back to the old
# settings plus KEY.
#
# Exit status: 0 done, or nothing to change (--check: KEY already has VALUE);
# 1 --check only, KEY would change; 3 the file is refused, with the reason on
# stderr and the file untouched. Any other status is an unexpected failure.
import json
import os
import stat
import sys
import tempfile

check = sys.argv[1] == "--check"
path, key, value = sys.argv[2:] if check else sys.argv[1:]


def refuse(reason):
    print(reason, file=sys.stderr)
    sys.exit(3)


def unique(pairs):
    result = {}
    for name, item in pairs:
        if name in result:
            raise ValueError("duplicate JSON key " + json.dumps(name))
        result[name] = item
    return result


def blank_jsonc(text):
    """Return text with comments and trailing commas replaced by spaces, so it
    is plain JSON with every offset unchanged, and the offsets of the trailing
    commas."""
    chars = list(text)
    trailing = []
    pending = None  # offset of a comma with only blanks since
    i = 0
    while i < len(chars):
        char = chars[i]
        if char == '"':
            pending = None
            i += 1
            while i < len(chars) and chars[i] != '"':
                i += 2 if chars[i] == "\\" else 1
            i += 1
            continue
        if char == "/" and i + 1 < len(chars) and chars[i + 1] in "/*":
            if chars[i + 1] == "/":
                end = i
                while end < len(chars) and chars[end] not in "\r\n":
                    end += 1
            else:
                end = text.find("*/", i + 2)
                if end < 0:
                    raise ValueError("a comment is never closed")
                end += 2
            for j in range(i, end):
                if chars[j] not in "\r\n":
                    chars[j] = " "
            i = end
            continue
        if char == ",":
            pending = i
        elif char in "}]":
            if pending is not None:
                chars[pending] = " "
                trailing.append(pending)
            pending = None
        elif char not in " \t\r\n":
            pending = None
        i += 1
    return "".join(chars), trailing


def skip(text, i):
    while i < len(text) and text[i] in " \t\r\n":
        i += 1
    return i


def members(text):
    """Offsets in the plain JSON object text: each member as (name, start of
    its key, start of its value, end of its value), and the closing brace."""
    found = []
    i = skip(text, 0)
    i = skip(text, i + 1)
    while text[i] != "}":
        key_start = i
        name, i = json.decoder.scanstring(text, i + 1)
        i = skip(text, i) + 1  # the colon
        start = skip(text, i)
        _, end = json.JSONDecoder().raw_decode(text, start)
        found.append((name, key_start, start, end))
        i = skip(text, end)
        if text[i] == ",":
            i = skip(text, i + 1)
    return found, i


def parse(text):
    plain, _ = blank_jsonc(text)
    data = json.loads(plain, object_pairs_hook=unique)
    if not isinstance(data, dict):
        raise ValueError("settings must be a JSON object")
    return data


def edit(text):
    """The text with KEY set to VALUE and nothing else changed."""
    plain, trailing = blank_jsonc(text)
    found, close = members(plain)
    new_line = "\r\n" if "\r\n" in text else "\n"
    encoded = json.dumps(value, ensure_ascii=False)
    for name, _, start, end in found:
        if name == key:
            return text[:start] + encoded + text[end:]
    member = json.dumps(key) + ": " + encoded
    if not found:
        return text[:close].rstrip() + new_line + "    " + member + new_line + text[close:]
    key_start, last_end = found[-1][1], found[-1][3]
    line_start = max(text.rfind("\n", 0, key_start), text.rfind("\r", 0, key_start)) + 1
    indent = text[line_start:key_start]
    if indent.strip():
        indent = "    "
    # Put the new member after the last one, with the comma that joins them. A
    # trailing comma already there is kept, and so is a comment on the last
    # member's line: the new member goes on the next line.
    comma = next((c for c in trailing if last_end <= c < close), None)
    anchor = last_end if comma is None else comma + 1
    joiner, closer = ("," if comma is None else ""), ("" if comma is None else ",")
    eol = anchor
    while eol < close and text[eol] not in "\r\n":
        eol += 1
    if eol < close and not plain[anchor:eol].strip():
        return (
            text[:last_end] + joiner + text[last_end:eol]
            + new_line + indent + member + closer + text[eol:]
        )
    return text[:last_end] + joiner + text[last_end:anchor] + " " + member + closer + text[anchor:]


try:
    previous = os.lstat(path)
except FileNotFoundError:
    if check:
        sys.exit(1)
    previous = None
if previous is not None and (not stat.S_ISREG(previous.st_mode) or previous.st_nlink != 1):
    refuse("it must be an ordinary file with a single link")

if previous is None:
    text, data, mode, blank = "", {}, 0o600, True
else:
    try:
        with open(path, "rb") as stream:
            text = stream.read().decode("utf-8")
        # VS Code reads an empty settings.json as no settings.
        blank = not text.removeprefix("﻿").strip()
        data = {} if blank else parse(text.removeprefix("﻿"))
    except (UnicodeDecodeError, ValueError) as error:
        refuse("it is not valid JSON or JSONC (" + str(error) + ")")
    mode = stat.S_IMODE(previous.st_mode)

if data.get(key) == value:
    sys.exit(0)
if check:
    sys.exit(1)

bom = "﻿" if text.startswith("﻿") else ""
body = text.removeprefix("﻿")
if blank:
    updated = json.dumps({key: value}, ensure_ascii=False, indent=4) + "\n"
else:
    try:
        updated = edit(body)
        expected = {**data, key: value}
        if parse(updated) != expected:
            raise ValueError("the edit did not give the old settings plus the new value")
    except (ValueError, IndexError) as error:
        refuse("it could not be updated without changing anything else (" + str(error) + ")")

fd, temporary = tempfile.mkstemp(prefix=".workbench-settings-", dir=os.path.dirname(path))
try:
    os.fchmod(fd, mode)
    with os.fdopen(fd, "wb") as stream:
        stream.write((bom + updated).encode("utf-8"))
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
