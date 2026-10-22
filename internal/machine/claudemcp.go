package machine

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// claudeMCPEntry is what the claude-mcp step compares of a Claude Code MCP
// server entry: its command, its arguments and its env. Only the values of the
// names a dispatch server sets are ever decoded.
type claudeMCPEntry struct {
	Command string                     `json:"command"`
	Args    []string                   `json:"args"`
	Env     map[string]json.RawMessage `json:"env"`
}

// claudeMCPServers are the MCP server entries Claude Code keeps in its
// .claude.json: user scope, and local scope by project path. An entry stays
// raw until it is looked at, so one of another shape fails only its own match.
type claudeMCPServers struct {
	User     map[string]json.RawMessage `json:"mcpServers"`
	Projects map[string]struct {
		Local map[string]json.RawMessage `json:"mcpServers"`
	} `json:"projects"`
}

// readClaudeMCPServers reads Claude Code's MCP server entries; a missing file
// has none.
func readClaudeMCPServers(home string) (claudeMCPServers, error) {
	var servers claudeMCPServers
	raw, err := os.ReadFile(costs.ClaudeJSON(home))
	if errors.Is(err, fs.ErrNotExist) {
		return servers, nil
	}
	if err != nil {
		return servers, err
	}
	return servers, json.Unmarshal(raw, &servers)
}

// registered reports whether a user-scope entry starts server: the same
// command, arguments and env, which sets NODE_VERSION alone.
func registered(raw json.RawMessage, server dispatchServer) bool {
	var entry claudeMCPEntry
	if json.Unmarshal(raw, &entry) != nil || entry.Command != server.Command ||
		!slices.Equal(entry.Args, server.Args) ||
		!slices.Equal(slices.Sorted(maps.Keys(entry.Env)), slices.Sorted(maps.Keys(server.Env))) {
		return false
	}
	for name, want := range server.Env {
		var value string
		if json.Unmarshal(entry.Env[name], &value) != nil || value != want {
			return false
		}
	}
	return true
}

// claudeMCPChanges is what the claude-mcp step changes in Claude Code's user
// scope, from one read of its .claude.json, so the probe and the run agree:
// the user entries to remove, then the wanted servers to add. With the work
// role a server is added only when no user entry starts it as wanted, and an
// entry of its name is removed first; without it, every dispatch entry there
// is removed. tokens says a replaced entry's env names a token.
type claudeMCPChanges struct {
	servers  claudeMCPServers
	wanted   []dispatchServer
	remove   []string
	register []dispatchServer
	tokens   bool
}

func claudeMCPPending(c operation.Context, a Answers) (claudeMCPChanges, error) {
	servers, err := readClaudeMCPServers(c.Home)
	if err != nil {
		return claudeMCPChanges{}, err
	}
	// The servers Codex renders, with the workbench in the bin folder this
	// apply resolved as their command.
	changes := claudeMCPChanges{
		servers: servers,
		wanted: dispatchServers(
			a,
			filepath.Join(c.Paths.Bin, "workbench"),
			c.Paths.Config,
			c.Home,
		),
	}
	if changes.wanted == nil {
		for _, server := range dispatchTools {
			if _, ok := servers.User[server.Name]; ok {
				changes.remove = append(changes.remove, server.Name)
			}
		}
		return changes, nil
	}
	for _, server := range changes.wanted {
		raw, ok := servers.User[server.Name]
		if ok && registered(raw, server) {
			continue
		}
		changes.register = append(changes.register, server)
		var entry claudeMCPEntry
		if ok {
			changes.remove = append(changes.remove, server.Name)
			_ = json.Unmarshal(raw, &entry)
		}
		for name := range entry.Env {
			changes.tokens = changes.tokens || strings.Contains(name, "TOKEN")
		}
	}
	return changes, nil
}

// ClaudeMCPProbe is the claude-mcp step's probe lines: what it registers or
// removes, a same-named local entry, which outranks the user one, and tokens a
// replaced entry held that work.env doesn't get. It names files, entries and
// projects, never a value.
func ClaudeMCPProbe(c operation.Context, a Answers) []string {
	changes, err := claudeMCPPending(c, a)
	if err != nil {
		return []string{
			"claude-mcp: ? " + c.ShowPath(costs.ClaudeJSON(c.Home)) +
				" can't be read, so which servers are registered is unknown",
		}
	}
	if changes.wanted == nil {
		if len(changes.remove) == 0 {
			return []string{"claude-mcp: = nothing to do"}
		}
		return []string{"claude-mcp: remove " + strings.Join(changes.remove, ", ")}
	}
	lines := []string{"claude-mcp: = registered"}
	if len(changes.register) > 0 {
		var names []string
		for _, server := range changes.register {
			names = append(names, server.Name)
		}
		lines = []string{"claude-mcp: register " + strings.Join(names, ", ")}
	}
	for _, project := range slices.Sorted(maps.Keys(changes.servers.Projects)) {
		for _, server := range changes.wanted {
			if _, ok := changes.servers.Projects[project].Local[server.Name]; ok {
				lines = append(lines, "claude-mcp: = a local entry for "+server.Name+" in "+
					c.ShowPath(project)+" outranks this one: run claude mcp remove --scope local "+
					server.Name)
			}
		}
	}
	if changes.tokens && !workTokensWaiting() {
		lines = append(
			lines,
			"claude-mcp: = the old entry's credentials are not carried into work.env",
		)
	}
	return lines
}

// ClaudeMCPCommands are what the claude-mcp step's script runs: the claude
// the shell runs ("" when Claude Code is not installed), the bin folder of
// nvm's default Node ("" when it has none), which an npm copy of claude needs
// on PATH, whether the first command is Claude Code's first run, and the
// claude arguments, in order: the removes, then the adds. It lists none when
// nothing needs changing, and fails when Claude Code's file can't be read
// rather than guess.
//
// Claude Code's first run rewrites the settings.json in its config folder
// once, then writes its .claude.json. So firstRun is set only when that folder
// is ~/.claude, whose settings.json chezmoi wrote earlier in the apply, and
// the .claude.json [costs.ClaudeJSON] names is not there yet.
func ClaudeMCPCommands(
	c operation.Context,
	a Answers,
) (claude, nodeBin string, firstRun bool, commands [][]string, err error) {
	changes, err := claudeMCPPending(c, a)
	if err != nil {
		return "", "", false, nil, operation.Fail(
			operation.ExitBlocked,
			"claude-mcp",
			c.ShowPath(costs.ClaudeJSON(c.Home))+" can't be read, so no server was changed",
		)
	}
	nodeBin = nvmDefaultBin(c.Home)
	if filepath.Clean(costs.ClaudeDir(c.Home)) == filepath.Join(c.Home, ".claude") {
		_, statErr := os.Lstat(costs.ClaudeJSON(c.Home))
		firstRun = errors.Is(statErr, fs.ErrNotExist)
	}
	at := slices.IndexFunc(agents, func(agent agent) bool { return agent.command == "claude" })
	if at >= 0 {
		if install, ok := findAgent(c.Home, nodeBin, agents[at]); ok {
			claude = install.path
		}
	}
	for _, name := range changes.remove {
		commands = append(commands, []string{"mcp", "remove", "--scope", "user", name})
	}
	for _, server := range changes.register {
		entry, _ := json.Marshal(struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}{"stdio", server.Command, server.Args, server.Env})
		commands = append(
			commands,
			[]string{"mcp", "add-json", "--scope", "user", server.Name, string(entry)},
		)
	}
	return claude, nodeBin, firstRun, commands, nil
}
