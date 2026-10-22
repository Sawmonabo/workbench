package machine

import (
	"path/filepath"
	"slices"
)

// dispatchServer is one dispatch MCP server as Codex and Claude Code both
// register it. The JSON names are the keys of the dispatch_servers answer.
type dispatchServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Approve []string          `json:"approve"`
}

// dispatchTools are the dispatch servers by name, each with the tools the agent
// runs without asking; dispatchServers fills in how each one starts.
var dispatchTools = []dispatchServer{
	{Name: "dispatch-atlassian", Approve: []string{
		"fetch_attachments", "fetch_changelog", "fetch_comments", "fetch_confluence_page",
		"fetch_epic_context", "fetch_ticket", "gather_full_context", "search_confluence",
		"search_tickets",
	}},
	{Name: "dispatch-gitlab", Approve: []string{"get_linked_mrs"}},
}

// dispatchServers are the dispatch servers of a machine with the work role, or
// none. Each runs `bin mcp-gate`, which starts the server only for a session
// inside the work folder, with the names in config's work.env. They are built
// in the work folder's dispatch_folder checkout, each in a folder of its own
// name. The server is launched through nvm-exec so it follows `nvm alias
// default` in either versions_mode; a ~/.nvm/versions/node/vX path exists
// only in pinned mode.
// Approve holds the tools the agent runs without asking.
func dispatchServers(a Answers, bin, config, home string) []dispatchServer {
	if a["has_work"] != true {
		return nil
	}
	work := a.folder(home, "work_folder")
	checkout := filepath.Join(work, a.text("dispatch_folder"))
	servers := slices.Clone(dispatchTools)
	for i := range servers {
		server := &servers[i]
		server.Command = bin
		server.Args = []string{
			"mcp-gate", "--folder", work, "--env-file", filepath.Join(config, "work.env"), "--",
			filepath.Join(home, ".nvm", "nvm-exec"), "node",
			filepath.Join(checkout, server.Name, "dist", "index.js"),
		}
		server.Env = map[string]string{"NODE_VERSION": "default"}
	}
	return servers
}
