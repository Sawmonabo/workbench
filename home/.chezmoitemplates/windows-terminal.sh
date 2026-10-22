# Windows Terminal's files, as both WSL scripts read them: terminal-adoption
# writes the whole settings.json and default-distro its defaultProfile, and
# both bind to the profile Terminal has for this distribution.
#
#   terminal_paths LOCALAPPDATA PROGRAMDATA
#       set WT_PACKAGE_DIR (Terminal's package folder), WT_SETTINGS_DIR (its
#       LocalState, with settings.json and state.json) and WT_FRAGMENT_DIRS
#       (where WSL and other apps put profile fragments) from the Linux paths
#       of those two Windows folders; an empty one names nothing
#   terminal_profile_start DISTRO
#       start reading which profile Terminal has for DISTRO (win_start); after
#       win_wait, terminal_profile_read sets WT_PROFILE_GUID, its guid ("" when
#       none is found), WT_PROFILE_NAME, its name, WT_PROFILE_MISSING, why
#       none was found ("" when one was: "none", or which profiles could each
#       be it), WT_WSL_PROFILES, how many WSL profiles Terminal has, and
#       WT_PROFILE, one JSON object with the guid, the profile's "source",
#       "commandline" true when it starts by a command line (a fragment's does,
#       which overrides startingDirectory), and "how" it was found. It fails
#       when the read did not finish.
#       The profile named DISTRO wins. Only when none is, a profile that
#       mentions DISTRO or its version (a renamed one, say) is taken, and only
#       when it is the one such profile: "Ubuntu" is part of "Ubuntu-24.04",
#       so a guess between several could bind another distribution's.
#   terminal_settings_start COPY
#       start copying settings.json to COPY, a local file (win_start), so what
#       reads it after win_wait reads the Windows drive once, under the cap;
#       WIN_RC[terminal-read] then says whether it did
#
# WSL >= 2.4.4 makes each distribution's profile a JSON fragment (the source
# is the fragment folder, Microsoft.WSL); older distributions get one from
# Terminal's own generator (Windows.Terminal.Wsl), merged into settings.json.
# Guessing guids fails across these cases, so they are read. Every read of
# these folders on the Windows drive is a Windows call (windows-calls.sh,
# included first), under the cap every Windows call has.
#
# Plain bash, with no template directive, like windows-writers.sh.

terminal_paths() {
    WT_PACKAGE_DIR="" WT_SETTINGS_DIR="" WT_FRAGMENT_DIRS=()
    if [ -n "$1" ]; then
        WT_PACKAGE_DIR="$1/Packages/Microsoft.WindowsTerminal_8wekyb3d8bbwe"
        WT_SETTINGS_DIR="$WT_PACKAGE_DIR/LocalState"
        WT_FRAGMENT_DIRS+=("$1/Microsoft/Windows Terminal/Fragments")
    fi
    [ -z "$2" ] || WT_FRAGMENT_DIRS+=("$2/Microsoft/Windows Terminal/Fragments")
}

# The profiles are read with json, which a settings.json with comments fails;
# its profiles then go unseen, and the fragments still count.
terminal_profile_start() {
    win_start terminal-profile python3 -I -c '
import glob, json, os, re, sys
wt, distro, *fragment_dirs = sys.argv[1:]
guid_form = re.compile(r"\{[0-9A-Fa-f]{8}(-[0-9A-Fa-f]{4}){3}-[0-9A-Fa-f]{12}\}")
def load(path):
    try:
        with open(path, encoding="utf-8-sig") as stream:
            return json.load(stream)
    except Exception:
        return {}
def listed(document):
    profiles = document.get("profiles", []) if isinstance(document, dict) else []
    if isinstance(profiles, dict):
        profiles = profiles.get("list", [])
    if not isinstance(profiles, list):
        return []
    return [p for p in profiles if isinstance(p, dict) and isinstance(p.get("guid"), str)
            and guid_form.fullmatch(p["guid"])]
version = re.search(r"\d+\.\d+", distro)
version = version.group(0) if version else distro
def mentions(profile):
    blob = json.dumps(profile)
    return distro in blob or version in blob
def one_line(text):
    return " ".join(str(text).split())
wsl, hidden = set(), set()
exact, loose = [], {}
def consider(p, candidate, loosely):
    if p.get("name") == distro:
        exact.append(candidate)
    elif loosely and mentions(p):
        loose.setdefault(p["guid"].lower(), candidate)
# 1. fragments: the source is the fragment folder name
for folder in fragment_dirs:
    for path in sorted(glob.glob(os.path.join(folder, "*", "*.json"))):
        source = os.path.basename(os.path.dirname(path))
        for p in listed(load(path)):
            if source == "Microsoft.WSL":
                wsl.add(p["guid"].lower())
            consider(p, {"guid": p["guid"], "source": source, "commandline": True,
                         "name": p.get("name"), "how": "bound to fragment " + path}, True)
# 2. profiles Terminal already merged into settings.json (its generator or a fragment)
state = load(os.path.join(wt, "state.json")) if wt else {}
generated = set(state.get("generatedProfiles", [])) if isinstance(state, dict) else set()
for name in ("settings.json", "settings.json.before-dotfiles") if wt else ():
    for p in listed(load(os.path.join(wt, name))):
        if name == "settings.json":
            if p.get("source") in ("Microsoft.WSL", "Windows.Terminal.Wsl"):
                wsl.add(p["guid"].lower())
            if p.get("hidden") is True:
                hidden.add(p["guid"].lower())
        if p.get("source"):
            consider(p, {"guid": p["guid"], "source": p["source"], "commandline": bool(p.get("commandline")),
                         "name": p.get("name"), "how": "bound to generated profile in " + name},
                     p["guid"] in generated)
missing = ""
if exact:
    found = exact[0]
elif len(loose) == 1:
    found = next(iter(loose.values()))
else:
    found = {"guid": "", "how": "no generated WSL profile found"}
    missing = "none"
    if loose:
        names = sorted(one_line(c.get("name") or c["guid"]) for c in loose.values())
        missing = "more than one profile could be it (" + ", ".join(names) + ")"
        found["how"] = "not bound: " + missing
if found["guid"]:
    wsl.add(found["guid"].lower())
name = found.get("name")
print(len(wsl - hidden))
print(found["guid"])
print(one_line(name) if isinstance(name, str) else "")
print(missing)
print(json.dumps(found))
' "${WT_SETTINGS_DIR:-}" "$1" "${WT_FRAGMENT_DIRS[@]}"
}

# Each script reads the values it needs of these.
# shellcheck disable=SC2034
terminal_profile_read() {
    local lines
    WT_WSL_PROFILES=0 WT_PROFILE_GUID="" WT_PROFILE_NAME="" WT_PROFILE_MISSING="" WT_PROFILE=""
    [ "${WIN_RC[terminal-profile]:-}" = 0 ] || return 1
    mapfile -t lines <<<"${WIN_OUT[terminal-profile]}"
    [ "${#lines[@]}" = 5 ] && [[ ${lines[0]} =~ ^[0-9]+$ ]] || return 1
    WT_WSL_PROFILES=${lines[0]} WT_PROFILE_GUID=${lines[1]} WT_PROFILE_NAME=${lines[2]}
    WT_PROFILE_MISSING=${lines[3]} WT_PROFILE=${lines[4]}
}

terminal_settings_start() {
    win_start terminal-read python3 -I -c '
import shutil, sys
shutil.copyfile(sys.argv[1], sys.argv[2])
' "$WT_SETTINGS_DIR/settings.json" "$1"
}
