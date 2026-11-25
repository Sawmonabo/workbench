package machine

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// runtimePins are the language runtime pins of versions.toml's [versions].
type runtimePins struct {
	Node         []string `toml:"node"`
	NodeDefault  string   `toml:"node_default"`
	PythonPinned []string `toml:"python_pinned"`
	Go           string   `toml:"go"`
	Rust         string   `toml:"rust"`
	Bun          string   `toml:"bun"`
}

// runtimeState is what doctor found for one runtime: whether it is installed,
// the version in use ("" when none is installed or it cannot be read), what
// installed it when the name alone does not say, the pin to show ("" where no
// pin applies, as for the Go and bun Homebrew installs), what an apply in
// pinned mode would still change ("" when nothing), a note that replaces the
// pin, and whether the step that installs it is skipped.
type runtimeState struct {
	name, have, from, pin, off, note string
	found, skipped                   bool
}

// checkRuntimes reports Node, Python, Rust, Go and bun against the pins of the
// selected source, reading the installers' own layout as the runtimes probe
// does, without running a version manager and without the network. In latest
// mode a version other than the pin is expected, so a line is complete
// whenever the runtime is installed; in pinned mode a line is pending when an
// apply would still install or switch to a pin. A missing runtime is absent,
// or skipped when its step is a saved skip. None of these lines is blocked:
// they are the machine's setup, which workbench apply brings in line, not a
// prerequisite Workbench needs.
func checkRuntimes(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
	skip []string,
) []operation.Component {
	var data struct {
		Versions runtimePins `toml:"versions"`
	}
	if err := toml.Unmarshal(files[versionsFile], &data); err != nil {
		return []operation.Component{{
			Name:    "runtimes",
			Status:  operation.StatusSkipped,
			Message: "The runtime pins in " + versionsFile + " cannot be read: " + err.Error(),
		}}
	}
	pins := data.Versions
	mode := ""
	if raw, err := ReadMachineConfig(c.Native.Config); err == nil {
		if answers, err := readAnswers(raw); err == nil {
			mode, _ = answers["versions_mode"].(string)
		}
	}
	skipped := func(effects ...string) bool {
		return slices.ContainsFunc(effects, func(e string) bool { return slices.Contains(skip, e) })
	}
	states := []runtimeState{
		nodeState(c.Home, mode, pins),
		pythonState(c.Home, mode, pins),
		rustState(c.Home, mode, pins),
		goState(c.Home, mode, pins),
		bunState(ctx, c, mode, pins),
	}
	states[0].skipped = skipped("runtime-managers", "runtimes")
	states[1].skipped = skipped("runtime-managers", "runtimes")
	states[2].skipped = skipped("runtime-managers", "runtimes")
	states[3].skipped = skipped("runtime-managers", "macos-packages")
	states[4].skipped = skipped("runtime-managers", "macos-packages")
	components := make([]operation.Component, 0, len(states))
	for _, state := range states {
		components = append(components, state.component(mode))
	}
	return components
}

// component is the doctor line of one runtime.
func (r runtimeState) component(mode string) operation.Component {
	line := operation.Component{Name: r.name, Status: operation.StatusComplete}
	shown := r.have + r.from
	switch {
	case r.found && r.have == "":
		line.Message = "installed; its version cannot be read"
	case r.have == "" && r.skipped:
		line.Status, line.Message = operation.StatusSkipped, "not installed; its step is skipped (saved)"
	case r.have == "":
		line.Status, line.Message = operation.StatusAbsent, "not installed; workbench apply installs it"
	case r.off != "" && r.skipped:
		line.Status = operation.StatusSkipped
		line.Message = shown + " (pinned " + r.pin + "); its step is skipped (saved)"
	case r.off != "":
		line.Status = operation.StatusPending
		line.Message = shown + " (pinned " + r.pin + "); workbench apply " + r.off
	case r.note != "":
		line.Message = shown + " (" + r.note + ")"
	case r.pin == "":
		line.Message = shown
	case mode == "pinned":
		line.Message = shown + " (pinned " + r.pin + ")"
	case mode == "latest":
		line.Message = shown + " (latest mode)"
	default:
		line.Message = shown + " (pin " + r.pin + ")"
	}
	return line
}

// nodeState reads nvm's default Node (nvmDefaultBin). In pinned mode the
// apply makes node_default the default and installs every pinned Node.
func nodeState(home, mode string, pins runtimePins) runtimeState {
	state := runtimeState{name: "runtime-node", pin: pins.NodeDefault}
	if bin := nvmDefaultBin(home); bin != "" {
		state.found = true
		state.have = strings.TrimPrefix(filepath.Base(filepath.Dir(bin)), "v")
	}
	if mode != "pinned" || state.have == "" {
		return state
	}
	var changes []string
	if state.have != pins.NodeDefault {
		changes = append(changes, "makes "+pins.NodeDefault+" the default")
	}
	var missing []string
	for _, version := range pins.Node {
		if !operation.LeadsToFolder(filepath.Join(home, ".nvm", "versions", "node", "v"+version)) {
			missing = append(missing, version)
		}
	}
	if len(missing) > 0 {
		changes = append(changes, "installs "+strings.Join(missing, ", "))
	}
	state.off = strings.Join(changes, " and ")
	return state
}

// uvPython is a Python uv installed: cpython-X.Y.Z-..., not the cpython-X.Y-...
// links uv keeps to the newest of each minor version.
var uvPython = regexp.MustCompile(`^cpython-([0-9]+\.[0-9]+\.[0-9]+)-`)

// pythonState reads the Pythons uv installed, in the folder the runtimes probe
// reads, and shows the newest. In pinned mode the apply installs every pinned
// Python.
func pythonState(home, mode string, pins runtimePins) runtimeState {
	state := runtimeState{
		name: "runtime-python", from: " from uv", pin: strings.Join(pins.PythonPinned, ", "),
	}
	folder := os.Getenv("UV_PYTHON_INSTALL_DIR")
	if folder == "" {
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		folder = filepath.Join(data, "uv", "python")
	}
	entries, _ := os.ReadDir(folder)
	var installed []string
	for _, entry := range entries {
		if match := uvPython.FindStringSubmatch(entry.Name()); match != nil && entry.IsDir() {
			installed = append(installed, match[1])
			state.found = true
			if state.have == "" || newerVersion(match[1], state.have) {
				state.have = match[1]
			}
		}
	}
	if mode != "pinned" || state.have == "" {
		return state
	}
	var missing []string
	for _, version := range pins.PythonPinned {
		if !slices.Contains(installed, version) {
			missing = append(missing, version)
		}
	}
	if len(missing) > 0 {
		state.off = "installs " + strings.Join(missing, ", ")
	}
	return state
}

// rustupHome is rustup's folder, as the runtimes probe finds it.
func rustupHome(home string) string {
	if folder := os.Getenv("RUSTUP_HOME"); folder != "" {
		return folder
	}
	return filepath.Join(home, ".rustup")
}

// rustToolchainVersion matches a toolchain named by its version, such as
// 1.93.0-aarch64-apple-darwin.
var rustToolchainVersion = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)-`)

// rustState reads rustup's default toolchain from its settings.toml, and its
// version from the name or, for a channel such as stable, from the
// toolchain's channel manifest; it runs no rustup proxy, which may download a
// toolchain. In pinned mode the apply installs the pinned toolchain and makes
// it the default.
func rustState(home, mode string, pins runtimePins) runtimeState {
	state := runtimeState{name: "runtime-rust", pin: pins.Rust}
	root := rustupHome(home)
	var settings struct {
		Default string `toml:"default_toolchain"`
	}
	if raw, err := os.ReadFile(filepath.Join(root, "settings.toml")); err == nil {
		_ = toml.Unmarshal(raw, &settings)
	}
	if settings.Default == "" ||
		!operation.LeadsToFolder(filepath.Join(root, "toolchains", settings.Default)) {
		return state
	}
	state.found = true
	if match := rustToolchainVersion.FindStringSubmatch(settings.Default); match != nil {
		state.have = match[1]
	} else {
		state.have = rustcVersion(filepath.Join(
			root,
			"toolchains",
			settings.Default,
			"lib",
			"rustlib",
			"multirust-channel-manifest.toml",
		))
	}
	if mode != "pinned" || state.have == "" {
		return state
	}
	pinned, _ := filepath.Glob(filepath.Join(root, "toolchains", pins.Rust+"-*"))
	switch {
	case len(pinned) == 0:
		state.off = "installs " + pins.Rust + " and makes it the default"
	case !strings.HasPrefix(settings.Default, pins.Rust+"-"):
		state.off = "makes " + pins.Rust + " the default"
	}
	return state
}

// rustcVersion reads the rustc version a toolchain's channel manifest
// records under [pkg.rustc], or "".
func rustcVersion(manifest string) string {
	file, err := os.Open(manifest)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	lines := bufio.NewScanner(file)
	lines.Buffer(make([]byte, 0, 64<<10), 1<<20)
	inRustc := false
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if strings.HasPrefix(line, "[") {
			inRustc = line == "[pkg.rustc]"
			continue
		}
		if value, ok := strings.CutPrefix(line, "version = "); ok && inRustc {
			return toolVersion.FindString(value)
		}
	}
	return ""
}

// findRuntime is the first executable named name in the folders the managed
// shell puts on PATH, after ~/.local/bin, where the Linux Runtime managers
// step links the Go it installs; "" when there is none.
func findRuntime(home, name string) string {
	folders := append(
		[]string{filepath.Join(home, ".local", "bin")},
		platform.ToolFolders(home, nvmDefaultBin(home))...,
	)
	for _, folder := range folders {
		path := filepath.Join(folder, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// goState reads the VERSION file of the Go the shell runs, as the Linux probe
// does, without running go, which may download a toolchain. In pinned mode
// the Linux apply installs the pin.
func goState(home, mode string, pins runtimePins) runtimeState {
	state := runtimeState{name: "runtime-go", pin: pins.Go}
	if path := findRuntime(home, "go"); path != "" {
		state.found = true
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			raw, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(resolved)), "VERSION"))
			first, _, _ := strings.Cut(string(raw), "\n")
			state.have = toolVersion.FindString(first)
		}
	}
	switch {
	case state.have == "":
	case platform.GoBunFromHomebrew:
		state.from, state.pin = " from Homebrew", ""
	case mode == "pinned" && state.have != pins.Go:
		state.off = "installs " + pins.Go
	}
	return state
}

// bunState asks the bun the shell runs for its version. The Linux apply
// installs bun only when it is missing, at the pin in pinned mode, and keeps
// an installed one, so a bun at another version is no change it would make.
func bunState(
	ctx context.Context,
	c operation.Context,
	mode string,
	pins runtimePins,
) runtimeState {
	state := runtimeState{name: "runtime-bun", pin: pins.Bun}
	if path := findRuntime(c.Home, "bun"); path != "" {
		state.found = true
		state.have, _ = probeVersion(ctx, c, "bun", path)
	}
	switch {
	case state.have == "":
	case platform.GoBunFromHomebrew:
		state.from, state.pin = " from Homebrew", ""
	case mode == "pinned" && state.have != pins.Bun:
		state.note = "pin " + pins.Bun + " is for a new install; workbench apply keeps an installed bun"
	}
	return state
}
