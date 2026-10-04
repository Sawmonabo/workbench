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
// coding-agent markers below, and nothing else of the caller's.
func Handoff(c Context, expected ReleaseRecord, args []string) error {
	state, err := ReadState(c.Paths)
	if err != nil {
		return err
	}
	if state == nil || state.ActiveRelease == nil || *state.ActiveRelease != expected {
		return Fail(
			ExitConflict,
			"handoff",
			"Activated runtime changed before handoff; run workbench doctor, then rerun workbench update",
		)
	}
	entry, err := os.Readlink(filepath.Join(c.Paths.Bin, "workbench"))
	if err != nil || entry != expected.Executable {
		return Fail(
			ExitConflict,
			"handoff",
			"Runtime entry point changed before handoff; rerun workbench update",
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
		"WORKBENCH_CONFIG_DIR=" + c.Paths.Config,
		"WORKBENCH_DATA_DIR=" + c.Paths.Data,
		"WORKBENCH_STATE_DIR=" + c.Paths.State,
		"WORKBENCH_CACHE_DIR=" + c.Paths.Cache,
		"WORKBENCH_BIN_DIR=" + c.Paths.Bin,
	}
	environment = append(environment, passedThrough(handoffVariables)...)
	environment = append(environment, NetworkEnvironment()...)
	environment = append(environment, passedThrough(AgentVariables)...)
	if err = syscall.Exec(
		executable,
		append([]string{executable}, args...),
		environment,
	); err != nil {
		return Fail(
			ExitFailed,
			"handoff",
			"Verified runtime could not start; installed and staged runtime files remain available; retry the selected command",
		)
	}
	return nil
}

// AgentVariables are the variables by which a coding agent marks the commands
// it runs: Claude Code sets CLAUDECODE=1, and Codex sets CODEX_CI,
// CODEX_THREAD_ID and, in its sandbox, CODEX_SANDBOX and
// CODEX_SANDBOX_NETWORK_DISABLED. The handoff carries them so the activated
// runtime still knows an agent runs it and never prompts or applies unasked.
// internal/cli's agentSession must test the same names.
var AgentVariables = []string{
	"CLAUDECODE",
	"CODEX_CI",
	"CODEX_THREAD_ID",
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
}

// networkVariables are the proxy and CA settings Go's HTTP transport, uv, curl
// and the tools behind them read. They describe the user's network, not a
// credential store or an executable lookup, so the explicit environments of
// subprocesses that download may carry them. Ecosystem-specific CA variables
// (NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE, CARGO_HTTP_CAINFO) stay out: Go,
// uv and curl do not read them, and each would widen what every subprocess
// inherits.
var networkVariables = []string{
	"HTTPS_PROXY", "https_proxy",
	"HTTP_PROXY", "http_proxy",
	"ALL_PROXY", "all_proxy",
	"NO_PROXY", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE",
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
