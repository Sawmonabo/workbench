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
// server entry: its command, its arguments and the names its env sets. The
// env values are never decoded.
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

// claudeMCPWanted are the dispatch servers as the claude-mcp step registers
// them: the ones Codex renders, with the workbench in the bin folder this
// apply resolved as their command.
func claudeMCPWanted(c operation.Context, a Answers) []dispatchServer {
	return dispatchServers(a, filepath.Join(c.Paths.Bin, "workbench"), c.Paths.Config, c.Home)
}

// dispatchNames are the dispatch servers' names, which a machine without the
// work role removes.
func dispatchNames() []string {
	var names []string
	for _, server := range dispatchServers(Answers{"has_work": true}, "", "", "") {
		names = append(names, server.Name)
	}
	return names
}

// registered reports whether a user-scope entry starts server: the same
// command and arguments, and an env that sets NODE_VERSION alone.
func registered(raw json.RawMessage, server dispatchServer) bool {
	var entry claudeMCPEntry
	return json.Unmarshal(raw, &entry) == nil && entry.Command == server.Command &&
		slices.Equal(entry.Args, server.Args) &&
		slices.Equal(slices.Sorted(maps.Keys(entry.Env)), []string{"NODE_VERSION"})
}

// ClaudeMCPProbe is the claude-mcp step's probe lines. With the work role it
// registers each dispatch server Claude Code's user scope doesn't start as
// wanted, warns of a same-named local entry, which outranks the user one, and
// of tokens a replaced entry held that work.env doesn't get. Without it, it
// removes the user-scope entries. It names files, entries and projects, never
// a value.
func ClaudeMCPProbe(c operation.Context, a Answers) []string {
	servers, err := readClaudeMCPServers(c.Home)
	if err != nil {
		return []string{
			"claude-mcp: ? " + c.ShowPath(costs.ClaudeJSON(c.Home)) +
				" can't be read, so which servers are registered is unknown",
		}
	}
	wanted := claudeMCPWanted(c, a)
	if wanted == nil {
		var present []string
		for _, name := range dispatchNames() {
			if _, ok := servers.User[name]; ok {
				present = append(present, name)
			}
		}
		if len(present) == 0 {
			return []string{"claude-mcp: = nothing to do"}
		}
		return []string{"claude-mcp: remove " + strings.Join(present, ", ")}
	}
	var register []string
	tokens := false
	for _, server := range wanted {
		raw, ok := servers.User[server.Name]
		if ok && registered(raw, server) {
			continue
		}
		register = append(register, server.Name)
		var entry claudeMCPEntry
		if ok && json.Unmarshal(raw, &entry) == nil {
			for name := range entry.Env {
				tokens = tokens || strings.Contains(name, "TOKEN")
			}
		}
	}
	lines := []string{"claude-mcp: = registered"}
	if len(register) > 0 {
		lines = []string{"claude-mcp: register " + strings.Join(register, ", ")}
	}
	for _, project := range slices.Sorted(maps.Keys(servers.Projects)) {
		for _, server := range wanted {
			if _, ok := servers.Projects[project].Local[server.Name]; ok {
				lines = append(lines, "claude-mcp: = a local entry for "+server.Name+" in "+
					c.ShowPath(project)+" outranks this one: run claude mcp remove --scope local "+
					server.Name)
			}
		}
	}
	if tokens && !workTokensWaiting() {
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
// on PATH, and the claude arguments, in order. With the work role each
// dispatch server is removed from user scope, which fails harmlessly when it
// is not there, then added back as wanted; without it, each is only removed.
func ClaudeMCPCommands(
	c operation.Context,
	a Answers,
) (claude, nodeBin string, commands [][]string) {
	nodeBin = nvmDefaultBin(c.Home)
	at := slices.IndexFunc(agents, func(agent agent) bool { return agent.command == "claude" })
	if install, ok := findAgent(c.Home, nodeBin, agents[at]); ok {
		claude = install.path
	}
	remove := func(name string) []string { return []string{"mcp", "remove", "--scope", "user", name} }
	wanted := claudeMCPWanted(c, a)
	if wanted == nil {
		for _, name := range dispatchNames() {
			commands = append(commands, remove(name))
		}
		return claude, nodeBin, commands
	}
	for _, server := range wanted {
		entry, _ := json.Marshal(struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}{"stdio", server.Command, server.Args, server.Env})
		commands = append(
			commands,
			remove(server.Name),
			[]string{"mcp", "add-json", "--scope", "user", server.Name, string(entry)},
		)
	}
	return claude, nodeBin, commands
}
