package machine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

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
// Code's), the exact version and, for npm, the nvm Node version it is in.
type agentUpdate struct {
	tool, method, version, node string
}

// agentInstall is the copy of an agent the shell runs: how it was installed,
// its version and, for npm, the nvm Node version it is in. A Homebrew copy
// has its cask instead of a method.
type agentInstall struct {
	path, method, version, node, cask string
}

// latestTimeout bounds each lookup of an agent's newest version.
const latestTimeout = 10 * time.Second

// planAgentUpdates adds one effect per agent whose installed copy is older
// than its target, so approving the plan approves the update: the pin in
// pinned mode, the newest release of its installer's channel in latest mode.
// An installed copy that is already as new is left as it is, so nothing is
// ever downgraded, and a missing agent is the global-tools step's to install.
// A copy Homebrew installed is handed to the Homebrew updates (agentCasks);
// a copy from anywhere else is named in a warning and left alone. Nothing is
// run: installs and versions are read from the installers' own layout.
func (p *preparation) planAgentUpdates(
	ctx context.Context,
	c operation.Context,
	files map[string][]byte,
	latest bool,
) {
	c.Step("checking Codex and Claude Code")
	var data struct {
		Versions map[string]any `toml:"versions"`
	}
	if toml.Unmarshal(files["home/.chezmoidata/versions.toml"], &data) != nil {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Codex and Claude Code updates were not checked; versions.toml could not be read",
		)
		return
	}
	nvm := nvmDefaultBin(c.Home)
	for _, agent := range agents {
		install, found := findAgent(c.Home, nvm, agent)
		switch {
		case !found:
			continue
		case install.cask != "":
			if runtime.GOOS == "darwin" {
				p.agentCasks = append(p.agentCasks, install.cask)
				continue
			}
		case install.method != "":
			p.planAgentUpdate(ctx, agent, install, latest, data.Versions[agent.pin])
			continue
		}
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			agent.name+" at "+install.path+" did not come from npm in nvm's default Node or from its own installer; Workbench leaves its updates to you",
		)
	}
}

// planAgentUpdate adds the update effect for one installed agent when its
// target is newer.
func (p *preparation) planAgentUpdate(
	ctx context.Context,
	agent agent,
	install agentInstall,
	latest bool,
	pin any,
) {
	target, _ := pin.(string)
	if latest {
		var err error
		if target, err = latestAgentVersion(ctx, agent, install.method); err != nil {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				agent.name+" updates were not checked; its newest version could not be looked up",
			)
			return
		}
	}
	if !plainVersion.MatchString(install.version) || !plainVersion.MatchString(target) {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			agent.name+" "+install.version+" was not compared with "+target+", which are not both plain version numbers; it is left as it is",
		)
		return
	}
	if !newerVersion(target, install.version) {
		return
	}
	versions := install.version + " → " + target
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
		effect.Description = "Update " + agent.name + " " + versions + " through npm"
		effect.Recovery = "external; npm replaces the previous version"
		effect.What = "Updates " + agent.name + " from " + install.version + " to " + target +
			" with npm install -g " + agent.npm + "@" + target + " in Node " + install.node +
			", the nvm Node it is installed in."
		effect.Touches = agent.npm + " in Node " + install.node + "'s global packages"
		effect.Undo = "Not reverted automatically: npm install -g " + agent.npm + "@" +
			install.version + " in the same Node brings back the previous version"
	case "standalone":
		effect.Description = "Update Codex " + versions + " through its standalone installer"
		effect.Recovery = "external; the previous release is kept"
		effect.What = "Updates Codex from " + install.version + " to " + target +
			" with OpenAI's standalone installer (chatgpt.com/codex/install.sh --release " + target +
			"). An install of a set release also turns off the standalone package's background updates; an install of the latest release turns them back on."
		effect.Touches = "~/.codex/packages/standalone and ~/.local/bin/codex"
		effect.Undo = "Not reverted automatically: the previous release stays in ~/.codex/packages/standalone/releases, and the installer's --release " +
			install.version + " selects it again"
	case "native":
		effect.Description = "Update Claude Code " + versions + " through its native installer"
		effect.Recovery = "external; the installer keeps the newest earlier versions"
		effect.What = "Updates Claude Code from " + install.version + " to " + target +
			" with claude install " + target + ", the native installer's own command. Claude Code's background updates may move it on later."
		effect.Touches = "~/.local/share/claude/versions and ~/.local/bin/claude"
		effect.Undo = "Not reverted automatically: claude install " + install.version +
			" brings back the previous version"
	}
	p.agentUpdates = append(
		p.agentUpdates,
		agentUpdate{tool: agent.tool, method: install.method, version: target, node: install.node},
	)
	p.Plan.Effects = append(p.Plan.Effects, effect)
}

// plainVersion is a version newerVersion can compare: three dotted numbers.
var plainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// findAgent returns the agent's copy the shell runs: the first one in the
// order the managed shell startup files put their folders on PATH (bun's
// globals, nvm's default Node, ~/.local/bin, then Homebrew's and the system's
// folders), and how it was installed, read from where its executable leads.
func findAgent(home, nvm string, agent agent) (agentInstall, bool) {
	folders := []string{
		filepath.Join(home, ".bun", "bin"),
		nvm,
		filepath.Join(home, ".local", "bin"),
	}
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

// nvmDefaultBin returns the bin folder of nvm's default Node, or "": the
// version alias/default names, or the newest installed one it matches, with
// "node" and "stable" matching any, and an alias naming another alias
// followed once.
func nvmDefaultBin(home string) string {
	root := filepath.Join(home, ".nvm")
	alias := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(root, "alias", name))
		return strings.TrimSpace(string(data))
	}
	want := alias("default")
	if other := alias(want); want != "" && other != "" {
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

// latestAgentVersion asks the channel the agent's installer installs
// "latest" from for its newest version: the npm registry's latest tag,
// Codex's standalone release channel, or Claude Code's native release channel.
func latestAgentVersion(ctx context.Context, agent agent, method string) (string, error) {
	switch method {
	case "standalone":
		var release struct {
			Tag string `json:"tag_name"`
		}
		err := fetchJSON(ctx, "https://releases.openai.com/codex/channels/latest", &release)
		version := strings.TrimPrefix(strings.TrimPrefix(release.Tag, "rust-v"), "v")
		return version, err
	case "native":
		body, err := fetch(ctx, "https://downloads.claude.ai/claude-code-releases/latest")
		return strings.TrimSpace(string(body)), err
	}
	var latest struct {
		Version string `json:"version"`
	}
	err := fetchJSON(ctx, "https://registry.npmjs.org/"+agent.npm+"/latest", &latest)
	return latest.Version, err
}

// fetchJSON decodes the JSON document at location into target.
func fetchJSON(ctx context.Context, location string, target any) error {
	body, err := fetch(ctx, location)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// fetch reads a small document over HTTPS within latestTimeout.
func fetch(ctx context.Context, location string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, latestTimeout)
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
		return nil, operation.Fail(
			operation.ExitFailed,
			"agents",
			"Version lookup failed: HTTP "+response.Status,
		)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

// agentEnvironment tells the agent updates script which updates the plan
// checks, each as TOOL:METHOD:VERSION, with :NODE after an npm one.
func (p *preparation) agentEnvironment() []string {
	var updates []string
	for _, update := range p.agentUpdates {
		if !slices.ContainsFunc(p.Plan.Effects, func(effect operation.Effect) bool {
			return effect.Name == updateEffectPrefix+update.tool && effect.Checked
		}) {
			continue
		}
		entry := update.tool + ":" + update.method + ":" + update.version
		if update.node != "" {
			entry += ":" + update.node
		}
		updates = append(updates, entry)
	}
	if len(updates) == 0 {
		return nil
	}
	return []string{"WORKBENCH_AGENT_UPDATES=" + strings.Join(updates, " ")}
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
