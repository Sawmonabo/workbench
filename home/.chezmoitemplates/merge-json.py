# Narrow JSON settings merge; invalid/JSONC input stops without replacement.
# Do not silently discard unrelated values, duplicate keys or original bytes.
import json
import os
import stat
import sys
import tempfile

path, key, value = sys.argv[1:]
def unique(pairs):
    result = {}
    for name, item in pairs:
        if name in result:
            raise ValueError("duplicate JSON key")
        result[name] = item
    return result
try:
    previous = os.lstat(path)
    if not stat.S_ISREG(previous.st_mode) or previous.st_nlink != 1:
        raise ValueError("settings require an ordinary single-link file")
    with open(path, encoding="utf-8-sig") as stream:
        data = json.load(stream, object_pairs_hook=unique)
    mode = stat.S_IMODE(previous.st_mode)
except FileNotFoundError:
    data, mode = {}, 0o600
if not isinstance(data, dict):
    raise ValueError("settings must be a JSON object; original retained")
data[key] = value
encoded = json.dumps(data, ensure_ascii=False, indent=4) + "\n"
json.loads(encoded)
fd, temporary = tempfile.mkstemp(prefix=".workbench-settings-", dir=os.path.dirname(path))
try:
    os.fchmod(fd, mode)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        stream.write(encoded)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
