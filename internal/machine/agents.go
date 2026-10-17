package machine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// agentUpdatesScript carries out the planned Codex and Claude Code updates;
// see selectScripts.
const agentUpdatesScript = "45-agent-updates"

// agent is a coding agent Workbench installs when it is missing and updates
// through whichever installer put the copy the shell runs.
type agent struct {
	// name is what the plan calls it, tool the update effect's suffix, command
	// its executable, pin its versions.toml key and npm its npm package.
	name, tool, command, pin, npm string
	// casks are the Homebrew casks it comes in.
	casks []string
}

var agents = []agent{
	{
		name: "Codex", tool: "codex", command: "codex", pin: "codex", npm: "@openai/codex",
		casks: []string{"codex"},
	},
	{
		name: "Claude Code", tool: "claude-code", command: "claude", pin: "claude",
		npm:   "@anthropic-ai/claude-code",
		casks: []string{"claude-code", "claude-code@latest"},
	},
}

// agentUpdate is a planned update, with what the apply script needs to carry
// it out: the installer (npm, standalone for Codex's, native for Claude
// Code's), the exact version, the installed version it replaces, for npm the
// nvm Node version it is in, and whether it follows the installer's latest
// release rather than naming one.
type agentUpdate struct {
	tool, method, version, from, node string
	latest                            bool
}

// agentInstall is the copy of an agent the shell runs: how it was installed,
// its version and, for npm, the nvm Node version it is in. A Homebrew copy
// has its cask instead of a method. A copy of a vendor installer's release
// run through a launcher the installer did not make has no method, and
// launcher names the one the installer makes.
type agentInstall struct {
	path, method, version, node, cask, launcher string
}

// latestTimeout bounds each lookup of an agent's newest version while
// planning, as the runtimes probe bounds its Node lookup: a lookup that fails
// only warns and the next plan looks again. manifestTimeout bounds each
// download of Claude Code's signed release manifest during the apply, as
// Codex's installer bounds its release metadata.
const (
	latestTimeout   = 3 * time.Second
	manifestTimeout = 30 * time.Second
)

// findAgents records the copy of each agent the shell runs (findAgent), which
// the Claude Code settings floor (claudeFloor) and the updates
// (planAgentUpdates) both go by. Nothing is run: installs and versions are
// read from the installers' own layout.
func (p *preparation) findAgents(home string) {
	nvm := nvmDefaultBin(home)
	p.agentInstalls = make([]agentInstall, len(agents))
	for i, agent := range agents {
		p.agentInstalls[i], _ = findAgent(home, nvm, agent)
	}
}

// agentPins reads the [versions] table of versions.toml.
func agentPins(files map[string][]byte) (map[string]any, error) {
	var data struct {
		Versions map[string]any `toml:"versions"`
	}
	err := toml.Unmarshal(files["home/.chezmoidata/versions.toml"], &data)
	return data.Versions, err
}

// claudeFloor returns the minimumVersion Workbench sets in Claude Code's
// settings in pinned mode, the floor below which Claude Code's own updates
// install nothing: the pin, or the version the shell's Claude Code is at while
// that is older. So the floor rises to the pin only once the update to it has
// run, and never stops Claude Code's own updates while it has not. Without a
// copy whose version can be read (none, a Homebrew one or an unknown one), or
// in latest mode, it returns "" and the settings get no floor from Workbench.
func (p *preparation) claudeFloor(files map[string][]byte, answers Answers) string {
	i := slices.IndexFunc(agents, func(agent agent) bool { return agent.tool == "claude-code" })
	if answers["versions_mode"] != "pinned" || p.agentInstalls == nil ||
		p.agentInstalls[i].method == "" {
		return ""
	}
	pins, err := agentPins(files)
	pin, _ := pins[agents[i].pin].(string)
	installed := p.agentInstalls[i].version
	if err != nil || !plainVersion.MatchString(pin) || !plainVersion.MatchString(installed) {
		return ""
	}
	if newerVersion(pin, installed) {
		return installed
	}
	return pin
}

// planAgentUpdates adds one effect per agent whose installed copy (findAgents)
// is older than its target, so approving the plan approves the update: the
// pin in pinned mode, the newest release of its installer's channel in latest
// mode (for Claude Code, the channel its settings name; see claudeUpdates). An
// installed copy that is already as new is left as it is, so nothing is ever
// downgraded, and a missing agent is the global-tools step's to install. A
// copy Homebrew installed is handed to the Homebrew updates (agentCasks); a
// copy from anywhere else is named in a warning and left alone. The
// latest-mode lookups run side by side.
func (p *preparation) planAgentUpdates(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
	latest bool,
) {
	c.Step("checking Codex and Claude Code")
	pins, err := agentPins(files)
	if err != nil {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Codex and Claude Code updates were not checked; versions.toml could not be read",
		)
		return
	}
	installs := make([]agentInstall, len(agents))
	targets := make([]string, len(agents))
	channels := make([]string, len(agents))
	failed := make([]bool, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		install := p.agentInstalls[i]
		switch {
		case install.path == "":
			continue
		case install.cask != "":
			if runtime.GOOS == "darwin" {
				p.agentCasks = append(p.agentCasks, install.cask)
				continue
			}
		case install.method != "":
			installs[i] = install
			if !latest {
				targets[i], _ = pins[agent.pin].(string)
				continue
			}
			channels[i] = "latest"
			if agent.tool == "claude-code" {
				channels[i], _ = claudeUpdates(c.Home)
			}
			wg.Go(func() {
				var err error
				targets[i], err = latestAgentVersion(ctx, agent, install.method, channels[i])
				failed[i] = err != nil || targets[i] == ""
			})
			continue
		}
		warning := agent.name + " at " + install.path + " did not come from npm in nvm's default Node or from its own installer; Workbench leaves its updates to you"
		if install.launcher != "" {
			warning = agent.name + " at " + install.path + " runs its installer's release through a launcher of your own, which the installer leaves alone (its own is " + install.launcher + "); Workbench leaves its updates to you"
		}
		p.Plan.Warnings = append(p.Plan.Warnings, warning)
	}
	wg.Wait()
	for i, agent := range agents {
		switch {
		case installs[i].method == "":
		case failed[i]:
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				agent.name+" updates were not checked; its newest version could not be looked up",
			)
		case targets[i] == "":
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				agent.name+" updates were not checked; versions.toml does not pin "+agent.pin+" to a version",
			)
		case installs[i].method == "native" && p.claudeFloorAbove(c.Home, targets[i]):
		default:
			p.planAgentUpdate(agent, installs[i], targets[i], channels[i])
		}
	}
}

// claudeFloorAbove reports, with a warning, a native Claude Code update to
// target that claude install would skip: one below the minimumVersion its
// settings set, for which the installer reports success and installs nothing.
func (p *preparation) claudeFloorAbove(home, target string) bool {
	_, minimum := claudeUpdates(home)
	if !plainVersion.MatchString(minimum) || !plainVersion.MatchString(target) ||
		!newerVersion(minimum, target) {
		return false
	}
	p.Plan.Warnings = append(
		p.Plan.Warnings,
		"Claude Code is not updated to "+target+": its settings set minimumVersion "+minimum+
			", below which claude install installs nothing",
	)
	return true
}

// planAgentUpdate adds the update effect for one installed agent when target
// is newer. channel names the release channel target came from in latest
// mode, and is "" in pinned mode.
func (p *preparation) planAgentUpdate(
	agent agent,
	install agentInstall,
	target, channel string,
) {
	newer, comparable := agentNewer(target, install.version)
	if !comparable {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			agent.name+" "+install.version+" was not compared with "+target+", which are not both version numbers; it is left as it is",
		)
		return
	}
	if !newer {
		return
	}
	versions := install.version + " → " + target
	source := ""
	if channel != "" {
		source = " from its " + channel + " channel"
	}
	effect := operation.Effect{
		Name:      updateEffectPrefix + agent.tool,
		Delta:     versions,
		Privilege: "user; network",
		Title:     "Update " + agent.name,
		Summary:   versions,
		RunsAs:    "you; downloads the update",
	}
	switch install.method {
	case "npm":
		effect.Description = "Update " + agent.name + " " + versions + " through npm" + source
		effect.Recovery = "external; npm replaces the previous version"
		effect.What = "Updates " + agent.name + " from " + install.version + " to " + target +
			" with npm install -g " + agent.npm + "@" + target + " in Node " + install.node +
			", the nvm Node it is installed in."
		effect.Touches = agent.npm + " in Node " + install.node + "'s global packages"
		effect.Undo = "Not reverted automatically: npm install -g " + agent.npm + "@" +
			install.version + " in the same Node brings back the previous version"
	case "standalone":
		effect.Description = "Update Codex " + versions + " through its standalone installer" + source
		effect.Recovery = "external; the previous release is kept"
		if channel != "" {
			effect.What = "Updates Codex from " + install.version + " to " + target +
				" with OpenAI's standalone installer (chatgpt.com/codex/install.sh --release latest), as codex update does, so Codex keeps updating itself in the background. The apply first checks that Codex's latest channel still offers " +
				target + ", and afterwards that " + target + " is the release installed; otherwise the update is blocked, and a newer release the installer took instead may remain installed."
		} else {
			effect.What = "Updates Codex from " + install.version + " to " + target +
				" with OpenAI's standalone installer (chatgpt.com/codex/install.sh --release " + target +
				"); afterwards " + target + " must be the release installed, or the update is blocked. Installing a set release removes the standalone package's auto-update-version marker, so Codex stops updating itself in the background and stays at the pin. codex update turns background updates back on; it installs the newest release and writes the marker again."
		}
		effect.Touches = "~/.codex/packages/standalone and ~/.local/bin/codex"
		effect.Undo = "Not reverted automatically: the previous release stays in ~/.codex/packages/standalone/releases, and the installer's --release " +
			install.version + " selects it again"
	case "native":
		effect.Description = "Update Claude Code " + versions + " through its native installer" + source
		effect.Recovery = "external; the installer keeps the newest earlier versions"
		effect.What = "Updates Claude Code from " + install.version + " to " + target +
			" with claude install " + target + ", the native installer's own command, which leaves its release channel (autoUpdatesChannel) as it is. The apply first checks the GPG signature on Anthropic's release manifest for " +
			target + ", then downloads " + target + "'s binary for this machine to a private folder and runs it only if it matches that manifest's SHA-256. claude install downloads " +
			target + " again itself; the binary it installs must match as well, or it is removed, " +
			install.version + " is put back and the update is blocked. Claude Code keeps updating itself in the background on its channel"
		if channel == "" {
			effect.What += ", never below the minimumVersion Workbench sets in its settings: the version installed while that is older than the pin, the pin once this update has run."
		} else {
			effect.What += "."
		}
		effect.Touches = "~/.local/share/claude/versions and ~/.local/bin/claude"
		effect.Undo = "Not reverted automatically: claude install " + install.version +
			" brings back the previous version"
	}
	p.agentUpdates = append(p.agentUpdates, agentUpdate{
		tool:    agent.tool,
		method:  install.method,
		version: target,
		from:    install.version,
		node:    install.node,
		latest:  channel != "",
	})
	p.Plan.Effects = append(p.Plan.Effects, effect)
}

// agentVersion is a version agentNewer can compare: three dotted numbers,
// optionally followed by a prerelease such as -alpha.3.
var agentVersion = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)(?:-([0-9A-Za-z.-]+))?$`)

// agentNewer reports whether version a is newer than b, and whether the two
// could be compared at all. A prerelease ranks below its own release, as in
// Semantic Versioning, so 0.163.0 is newer than 0.163.0-alpha.3; two
// prereleases of the same release are not ranked, so neither is newer.
func agentNewer(a, b string) (newer, comparable bool) {
	left, right := agentVersion.FindStringSubmatch(a), agentVersion.FindStringSubmatch(b)
	if left == nil || right == nil {
		return false, false
	}
	if left[1] != right[1] {
		return newerVersion(left[1], right[1]), true
	}
	return left[2] == "" && right[2] != "", true
}

// findAgent returns the agent's copy the shell runs: the first one in the
// order the managed shell startup files put their folders on PATH (on Linux
// ~/bin, then bun's globals, nvm's default Node, Cargo's and Go's bin
// folders, ~/.local/bin, then Homebrew's and the system's folders), and how it
// was installed, read from where its executable leads. A vendor installer
// updates only the launcher it makes, ~/.local/bin/COMMAND, so a release of
// its reached through any other launcher keeps no method.
func findAgent(home, nvm string, agent agent) (agentInstall, bool) {
	var folders []string
	if runtime.GOOS != "darwin" {
		folders = append(folders, filepath.Join(home, "bin"))
	}
	folders = append(
		folders,
		filepath.Join(home, ".bun", "bin"),
		nvm,
		filepath.Join(home, ".cargo", "bin"),
		"/usr/local/go/bin",
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".local", "bin"),
	)
	if runtime.GOOS == "darwin" {
		folders = append(folders, "/opt/homebrew/bin", "/usr/local/bin")
	} else {
		folders = append(folders, "/home/linuxbrew/.linuxbrew/bin", "/usr/local/bin")
	}
	folders = append(folders, "/usr/bin", "/bin")
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		path := filepath.Join(folder, agent.command)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		install := agentInstall{path: path}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			install.method, install.version, install.node, install.cask = classifyAgent(
				home,
				resolved,
				agent,
			)
		}
		launcher := filepath.Join(home, ".local", "bin", agent.command)
		if (install.method == "native" || install.method == "standalone") && path != launcher {
			install = agentInstall{path: path, launcher: launcher}
		}
		return install, true
	}
	return agentInstall{}, false
}

// classifyAgent reads how the executable at resolved was installed:
//   - npm in an nvm Node: .nvm/versions/node/NODE/lib/node_modules/PACKAGE/...,
//     the version from the package's package.json;
//   - Codex's standalone installer: .codex/packages/standalone/releases/RELEASE/...,
//     the version from the release's codex-package.json, or else the release
//     name, VERSION-TARGET;
//   - Claude Code's native installer: .local/share/claude/versions/VERSION;
//   - Homebrew: a Caskroom/CASK/... folder of one of the agent's casks.
//
// Anything else returns nothing.
func classifyAgent(home, resolved string, agent agent) (method, version, node, cask string) {
	nodes := filepath.Join(home, ".nvm", "versions", "node") + "/"
	if rest, ok := strings.CutPrefix(resolved, nodes); ok {
		node, rest, _ = strings.Cut(rest, "/")
		modules := "lib/node_modules/" + agent.npm + "/"
		if !strings.HasPrefix(rest, modules) {
			return "", "", "", ""
		}
		var manifest struct {
			Version string `json:"version"`
		}
		data, err := os.ReadFile(filepath.Join(nodes, node, modules, "package.json"))
		if err != nil || json.Unmarshal(data, &manifest) != nil {
			return "", "", "", ""
		}
		return "npm", manifest.Version, node, ""
	}
	releases := filepath.Join(home, ".codex", "packages", "standalone", "releases") + "/"
	if rest, ok := strings.CutPrefix(resolved, releases); ok && agent.tool == "codex" {
		release, _, _ := strings.Cut(rest, "/")
		var manifest struct {
			Version string `json:"version"`
		}
		data, err := os.ReadFile(filepath.Join(releases, release, "codex-package.json"))
		if err == nil && json.Unmarshal(data, &manifest) == nil && manifest.Version != "" {
			return "standalone", manifest.Version, "", ""
		}
		for _, arch := range []string{"-aarch64-", "-x86_64-"} {
			if i := strings.Index(release, arch); i > 0 {
				return "standalone", release[:i], "", ""
			}
		}
		return "", "", "", ""
	}
	versions := filepath.Join(home, ".local", "share", "claude", "versions") + "/"
	if rest, ok := strings.CutPrefix(resolved, versions); ok && agent.tool == "claude-code" &&
		!strings.Contains(rest, "/") {
		return "native", rest, "", ""
	}
	if _, rest, ok := strings.Cut(resolved, "/Caskroom/"); ok {
		token, _, _ := strings.Cut(rest, "/")
		if slices.Contains(agent.casks, token) {
			return "", "", "", token
		}
	}
	return "", "", "", ""
}

// nvmAliasHops bounds how many aliases nvmDefaultBin follows, as an alias
// file naming itself would otherwise loop.
const nvmAliasHops = 5

// nvmDefaultBin returns the bin folder of nvm's default Node, or "": the
// version alias/default names, or the newest installed one it matches, with
// "node" and "stable" matching any. An alias naming another alias, such as
// lts/* naming lts/jod, is followed the way nvm follows it.
func nvmDefaultBin(home string) string {
	root := filepath.Join(home, ".nvm")
	alias := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(root, "alias", name))
		return strings.TrimSpace(string(data))
	}
	want := alias("default")
	for range nvmAliasHops {
		other := alias(want)
		if want == "" || other == "" {
			break
		}
		want = other
	}
	want = strings.TrimPrefix(want, "v")
	entries, _ := os.ReadDir(filepath.Join(root, "versions", "node"))
	best := ""
	for _, entry := range entries {
		version := strings.TrimPrefix(entry.Name(), "v")
		if !plainVersion.MatchString(version) {
			continue
		}
		if want != "node" && want != "stable" && version != want &&
			!strings.HasPrefix(version, want+".") {
			continue
		}
		if best == "" || newerVersion(version, best) {
			best = version
		}
	}
	if want == "" || best == "" {
		return ""
	}
	return filepath.Join(root, "versions", "node", "v"+best, "bin")
}

// plainVersion is a Node version newerVersion can compare: three dotted
// numbers.
var plainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// claudeUpdates returns the release channel Claude Code's own updates follow,
// "stable" or "latest", and the minimumVersion below which its updates and
// claude install install nothing, or "": each from the user's settings (in
// CLAUDE_CONFIG_DIR when that is set), overridden by the machine's managed
// settings files (managed-settings.json, then each managed-settings.d/*.json
// in name order), the way Claude Code merges them. Project and local settings
// depend on the folder Claude Code starts in and are not read. A channel
// unset or anything else means latest, Claude Code's default.
func claudeUpdates(home string) (channel, minimum string) {
	read := func(path string) {
		var settings struct {
			Channel *string `json:"autoUpdatesChannel"`
			Minimum *string `json:"minimumVersion"`
		}
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &settings) != nil {
			return
		}
		if settings.Channel != nil {
			channel = *settings.Channel
		}
		if settings.Minimum != nil {
			minimum = *settings.Minimum
		}
	}
	read(filepath.Join(costs.ClaudeDir(home), "settings.json"))
	managed := "/etc/claude-code"
	if runtime.GOOS == "darwin" {
		managed = "/Library/Application Support/ClaudeCode"
	}
	read(filepath.Join(managed, "managed-settings.json"))
	drops, _ := filepath.Glob(filepath.Join(managed, "managed-settings.d", "*.json"))
	for _, drop := range drops {
		if !strings.HasPrefix(filepath.Base(drop), ".") {
			read(drop)
		}
	}
	if channel != "stable" {
		channel = "latest"
	}
	return channel, minimum
}

// latestAgentVersion asks the channel the agent's installer installs from for
// its newest version: the npm registry's dist-tag named channel, Codex's
// standalone latest channel, or Claude Code's native release channel.
func latestAgentVersion(
	ctx context.Context,
	agent agent,
	method, channel string,
) (string, error) {
	switch method {
	case "standalone":
		var release struct {
			Tag string `json:"tag_name"`
		}
		err := fetchJSON(ctx, "https://releases.openai.com/codex/channels/latest", &release)
		version := strings.TrimPrefix(strings.TrimPrefix(release.Tag, "rust-v"), "v")
		return version, err
	case "native":
		body, err := fetch(
			ctx,
			"https://downloads.claude.ai/claude-code-releases/"+channel,
			latestTimeout,
		)
		return strings.TrimSpace(string(body)), err
	}
	var latest struct {
		Version string `json:"version"`
	}
	err := fetchJSON(ctx, "https://registry.npmjs.org/"+agent.npm+"/"+channel, &latest)
	return latest.Version, err
}

// fetchJSON decodes the JSON document at location, fetched within
// latestTimeout, into target.
func fetchJSON(ctx context.Context, location string, target any) error {
	body, err := fetch(ctx, location, latestTimeout)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// fetch reads a small document over HTTPS within timeout.
func fetch(ctx context.Context, location string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %s", location, response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

// claudeSigningKey is the fingerprint of the key that signs Claude Code's
// release manifests, as Anthropic's setup documentation lists it.
const claudeSigningKey = "31ddde24ddfab679f42d7bd2baa929ff1a7ecace"

// claudeChecksums returns, as PLATFORM=SHA256 pairs, the SHA-256 of each
// platform's Claude Code binary in the release manifest of version, once the
// manifest's detached GPG signature checks out against the release signing
// key: the key is fetched from its published address and used only when its
// fingerprint is claudeSigningKey. These are the steps Anthropic documents for
// checking a native install (code.claude.com/docs/en/setup, binary
// integrity). The apply script names the host's platform the way Claude
// Code's install.sh does and accepts that platform's value only.
func claudeChecksums(ctx context.Context, version string) ([]string, error) {
	base := "https://downloads.claude.ai/claude-code-releases/" + version + "/manifest.json"
	documents := map[string][]byte{}
	for _, location := range []string{
		"https://downloads.claude.ai/keys/claude-code.asc",
		base,
		base + ".sig",
	} {
		body, err := fetch(ctx, location, manifestTimeout)
		if err != nil {
			return nil, err
		}
		documents[location] = body
	}
	keys, err := openpgp.ReadArmoredKeyRing(
		bytes.NewReader(documents["https://downloads.claude.ai/keys/claude-code.asc"]),
	)
	if err != nil {
		return nil, err
	}
	keys = slices.DeleteFunc(keys, func(key *openpgp.Entity) bool {
		return hex.EncodeToString(key.PrimaryKey.Fingerprint) != claudeSigningKey
	})
	if len(keys) == 0 {
		return nil, fmt.Errorf(
			"the published key is not the release signing key %s",
			claudeSigningKey,
		)
	}
	if _, err = openpgp.CheckArmoredDetachedSignature(
		keys,
		bytes.NewReader(documents[base]),
		bytes.NewReader(documents[base+".sig"]),
		nil,
	); err != nil {
		return nil, err
	}
	var manifest struct {
		Version   string `json:"version"`
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
		} `json:"platforms"`
	}
	if err = json.Unmarshal(documents[base], &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != version {
		return nil, fmt.Errorf("the signed manifest is for %q", manifest.Version)
	}
	var checksums []string
	for name, platform := range manifest.Platforms {
		if platformName.MatchString(name) && sha256Hex.MatchString(platform.Checksum) {
			checksums = append(checksums, name+"="+platform.Checksum)
		}
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("the signed manifest lists no SHA-256")
	}
	slices.Sort(checksums)
	return checksums, nil
}

// sha256Hex is a SHA-256 written as lowercase hex, and platformName a
// manifest platform such as linux-x64-musl.
var (
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	platformName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)+$`)
)

// agentEnvironment tells the agent updates script which updates the plan
// checks, each as TOOL:METHOD:VERSION:FROM, with :NODE after an npm one; a
// Codex update of the latest release has the method standalone-latest. For a
// checked native Claude Code update it first reads the signed release
// manifest (claudeChecksums) and passes its PLATFORM=SHA256 pairs as
// WORKBENCH_CLAUDE_CODE_SHA256; when that fails, the update is left out and
// reported blocked in report, the file the scripts report to.
func (p *preparation) agentEnvironment(
	ctx context.Context,
	c operation.Context,
	report string,
) ([]string, error) {
	var updates, environment []string
	for _, update := range p.agentUpdates {
		name := updateEffectPrefix + update.tool
		if !slices.ContainsFunc(p.Plan.Effects, func(effect operation.Effect) bool {
			return effect.Name == name && effect.Checked
		}) {
			continue
		}
		method := update.method
		switch {
		case method == "standalone" && update.latest:
			method = "standalone-latest"
		case method == "native":
			stop := c.ShowProgress(
				"Checking Claude Code " + update.version + "'s signed release manifest",
			)
			checksums, err := claudeChecksums(ctx, update.version)
			stop()
			if err != nil {
				if err = reportBlocked(report, name, fmt.Sprintf(
					"Claude Code was not updated to %s: its signed release manifest could not be checked (%s), so the next apply tries again",
					update.version,
					strings.Join(strings.Fields(err.Error()), " "),
				)); err != nil {
					return nil, err
				}
				continue
			}
			environment = append(
				environment,
				"WORKBENCH_CLAUDE_CODE_SHA256="+strings.Join(checksums, " "),
			)
		}
		entry := update.tool + ":" + method + ":" + update.version + ":" + update.from
		if update.node != "" {
			entry += ":" + update.node
		}
		updates = append(updates, entry)
	}
	if len(updates) == 0 {
		return environment, nil
	}
	return append(environment, "WORKBENCH_AGENT_UPDATES="+strings.Join(updates, " ")), nil
}

// reportBlocked records in report, the file the scripts report to, that the
// effect name could not be done, as the scripts' part_blocked does.
func reportBlocked(report, name, reason string) error {
	file, err := os.OpenFile(report, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "%s\t%s\n", name, reason)
	return errors.Join(err, file.Close())
}

// updateScript returns the script that carries out the update effect name:
// the agent updates script for a planned agent update, otherwise the
// Homebrew updates script. Any other effect returns "".
func (p *preparation) updateScript(name string) string {
	tool, ok := strings.CutPrefix(name, updateEffectPrefix)
	switch {
	case !ok:
		return ""
	case slices.ContainsFunc(p.agentUpdates, func(update agentUpdate) bool {
		return update.tool == tool
	}):
		return agentUpdatesScript
	}
	return updatesScript
}
