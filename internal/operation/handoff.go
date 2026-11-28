package operation

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Handoff replaces this process with the activated runtime only after the
// caller has released operation locks and verified its release-check
// response. It is not a forwarding launcher and never grants consent to a
// later setup/apply. The runtime gets a rebuilt environment: Workbench's own
// paths, the presentation settings, the proxy and CA settings and the
// coding-agent markers (agentVariables), and nothing else of the caller's.
//
// ready runs once every check has passed, just before the runtime replaces
// this process, for the caller to finish with the terminal.
func Handoff(c Context, expected ReleaseRecord, args []string, ready func()) error {
	state, err := ReadState(c.Paths)
	if err != nil {
		return err
	}
	if state == nil || state.ActiveRelease == nil || *state.ActiveRelease != expected {
		return Fail(
			ExitConflict,
			"handoff",
			"Activated runtime changed before handoff",
			Fix("workbench", "doctor"),
			Fix("workbench", "update"),
		)
	}
	entry, err := os.Readlink(c.Paths.Entrypoint())
	if err != nil || entry != expected.Executable {
		return Fail(
			ExitConflict,
			"handoff",
			"Runtime entry point changed before handoff",
			Fix("workbench", "update"),
		)
	}
	executable, err := trustedExecutable(expected.Executable, nil)
	if err != nil {
		return err
	}
	if executable != expected.Executable {
		return Fail(ExitConflict, "handoff", "Activated executable path is not canonical")
	}
	var search []string
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(directory) {
			continue
		}
		canonical, pathErr := ExistingDirectory(directory)
		if pathErr != nil {
			continue
		}
		if outsideProjects(canonical, nil) == nil {
			search = append(search, canonical)
		}
	}
	if len(search) == 0 {
		search = []string{"/usr/bin", "/bin"}
	}
	environment := []string{
		"HOME=" + c.Home,
		"PATH=" + strings.Join(search, string(os.PathListSeparator)),
	}
	environment = append(environment, c.Paths.RuntimeVariables()...)
	environment = append(environment, "WORKBENCH_BIN_DIR="+c.Paths.Bin)
	environment = append(environment, passedThrough(handoffVariables)...)
	environment = append(environment, NetworkEnvironment()...)
	environment = append(environment, passedThrough(agentVariables)...)
	ready()
	_ = Exec(executable, args, environment)
	return Fail(
		ExitFailed,
		"handoff",
		"Verified runtime could not start",
		Line{Label: "kept", Text: "the installed and staged runtime files"},
		FixText("run the same command again"),
	)
}

// Exec replaces this process with command, which gets args and exactly
// environment, and returns only when command could not start. mcp-gate starts
// a work MCP server through it, so no Workbench process stays between the
// agent and the server.
func Exec(command string, args, environment []string) error {
	err := syscall.Exec(command, append([]string{command}, args...), environment)
	return Fail(ExitFailed, "exec", command+" could not start: "+err.Error())
}

// networkVariables are the proxy and CA settings of the tools that download
// for Workbench: Go's HTTP transport, uv and curl read the proxy variables and
// SSL_CERT_FILE, SSL_CERT_DIR or CURL_CA_BUNDLE; the setup scripts' npm and
// Node tools read NODE_EXTRA_CA_CERTS, cargo reads CARGO_HTTP_CAINFO, and the
// tmux plugin clone reads GIT_SSL_CAINFO. They describe the user's network,
// not a credential store or an executable lookup, so the explicit
// environments of subprocesses that download may carry them.
var networkVariables = []string{
	"HTTPS_PROXY", "https_proxy",
	"HTTP_PROXY", "http_proxy",
	"ALL_PROXY", "all_proxy",
	"NO_PROXY", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE",
	"NODE_EXTRA_CA_CERTS", "CARGO_HTTP_CAINFO", "GIT_SSL_CAINFO",
}

// handoffVariables are the settings the activated runtime reads to look like
// the runtime that started it: terminal type, locale, color and width
// choices, the temporary directory its scratch space lives in, and the WSL
// variables Windows host steps need.
var handoffVariables = []string{
	"TERM", "COLORTERM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "COLUMNS",
	"LANG", "LC_ALL", "LC_CTYPE", "TMPDIR",
	"WSL_INTEROP", "WSL_DISTRO_NAME", "WSLENV",
}

// NetworkEnvironment returns the proxy and CA variables that are set, as
// NAME=value entries to append to the explicit environment of a subprocess that
// downloads. The values are the user's own settings and may hold a proxy
// password, so nothing may print them; subprocess diagnostics are redacted
// output, never an environment dump.
func NetworkEnvironment() []string { return passedThrough(networkVariables) }

// passedThrough returns NAME=value for each listed variable that is set and not empty.
func passedThrough(names []string) []string {
	var environment []string
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
