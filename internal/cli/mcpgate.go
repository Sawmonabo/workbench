package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// mcpGateCommand starts a work MCP server only for an agent session inside the
// work folder, with the names in the env file added to its environment. Any
// other session gets a quiet MCP server with nothing to offer, and no file is
// read. The agents register the dispatch servers through it
// (internal/machine/dispatch.go).
func mcpGateCommand(o *options) *cobra.Command {
	var folder, envFile string
	cmd := &cobra.Command{
		Use:    "mcp-gate --folder DIR --env-file FILE -- COMMAND ARGS...",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
	}
	cmd.Flags().StringVar(&folder, "folder", "", "The work folder")
	cmd.Flags().StringVar(&envFile, "env-file", "", "The env file the server's names come from")
	_ = cmd.MarkFlagRequired("folder")
	_ = cmd.MarkFlagRequired("env-file")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil || !gateInside(folder, os.Getenv("CLAUDE_PROJECT_DIR"), cwd) {
			return quietServer(cmd.InOrStdin(), cmd.OutOrStdout())
		}
		data, err := operation.ReadPrivateInput(envFile, 1<<20)
		if err != nil {
			return o.filterFailure(cmd, operation.Fail(
				operation.ExitFailed, "mcp_gate", envFile+" is missing or readable by others",
			), "mcp-gate: ")
		}
		pairs, bad := machine.ParseEnvFile(data)
		for _, number := range bad {
			_, _ = fmt.Fprintf(
				cmd.ErrOrStderr(),
				"mcp-gate: %s line %d is not NAME=value and is skipped\n", envFile, number,
			)
		}
		environment := os.Environ()
		for _, pair := range pairs {
			environment = slices.DeleteFunc(environment, func(entry string) bool {
				return strings.HasPrefix(entry, pair[0]+"=")
			})
			environment = append(environment, pair[0]+"="+pair[1])
		}
		return o.filterFailure(cmd, operation.Exec(args[0], args[1:], environment), "mcp-gate: ")
	}
	return cmd
}

// gateInside reports whether the session's folder, projectDir when it is set
// and cwd otherwise, is folder or inside it. Both are compared with their
// symlinks resolved and by whole path segments, so a sibling such as
// folder-old is outside; a path that cannot be resolved to an absolute one is
// outside.
func gateInside(folder, projectDir, cwd string) bool {
	path := cwd
	if projectDir != "" {
		path = projectDir
	}
	folder, folderErr := filepath.EvalSymlinks(folder)
	path, pathErr := filepath.EvalSymlinks(path)
	if folderErr != nil || pathErr != nil || !filepath.IsAbs(folder) || !filepath.IsAbs(path) {
		return false
	}
	return path == folder || strings.HasPrefix(path, folder+"/")
}

// gateRequest is one newline-delimited JSON-RPC message from the agent. A
// message without an id is a notification, which gets no reply.
type gateRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// gateLists are the list requests the quiet server answers, each with the
// member that holds its empty list.
var gateLists = map[string]string{
	"tools/list":               "tools",
	"prompts/list":             "prompts",
	"resources/list":           "resources",
	"resources/templates/list": "resourceTemplates",
}

// gateError is a JSON-RPC error.
type gateError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// quietServer answers the agent as an MCP server with no capabilities and
// empty lists until its input ends. Codex asks for the tool list whatever the
// server declares and fails the server's startup on an error, so the lists
// are answered rather than refused. A line that is not a JSON-RPC message
// gets no reply.
func quietServer(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	for {
		line, err := reader.ReadBytes('\n')
		var request gateRequest
		if len(line) > 0 && json.Unmarshal(line, &request) == nil && request.ID != nil {
			if writeErr := encoder.Encode(gateReply(request)); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// gateReply is the quiet server's reply to request.
func gateReply(request gateRequest) map[string]any {
	reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	switch {
	case request.Method == "initialize":
		var params struct {
			ProtocolVersion json.RawMessage `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		reply["result"] = map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{},
			"serverInfo": map[string]string{
				"name":    "workbench-mcp-gate",
				"version": shownVersion(),
			},
		}
	case request.Method == "ping":
		reply["result"] = map[string]any{}
	case gateLists[request.Method] != "":
		reply["result"] = map[string][]any{gateLists[request.Method]: {}}
	default:
		reply["error"] = gateError{-32601, "Method not found"}
	}
	return reply
}
