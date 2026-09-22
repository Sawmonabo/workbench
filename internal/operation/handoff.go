package operation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Handoff replaces this process only after the caller has released operation
// locks and verified the activated candidate's release-check response. It is
// not a forwarding launcher and never grants consent to a later setup/apply.
func Handoff(c Context, expected ReleaseRecord, args []string, candidateRecordDigest string) error {
	state, err := ReadState(c.Paths)
	if err != nil {
		return err
	}
	if candidateRecordDigest != "" {
		raw, readErr := ReadPrivateInput(filepath.Join(c.Paths.State, "candidate.json"), 1<<20)
		if readErr != nil {
			return readErr
		}
		hash := sha256.Sum256(raw)
		if hex.EncodeToString(hash[:]) != candidateRecordDigest || expected.Source != c.Native.Source || !Within(filepath.Join(c.Paths.Data, "releases"), expected.Source) {
			return Fail(4, "handoff", "Staged candidate changed before offline handoff; select and review it again")
		}
	} else {
		if state == nil || state.ActiveRelease == nil || *state.ActiveRelease != expected {
			return Fail(4, "handoff", "Activated runtime changed before handoff; inspect state and resume installation")
		}
		entry, err := os.Readlink(filepath.Join(c.Paths.Bin, "workbench"))
		if err != nil || entry != expected.Executable {
			return Fail(4, "handoff", "Runtime entry point changed before handoff; resume installation")
		}
	}
	executable, err := trustedExecutable(expected.Executable, nil)
	if err != nil {
		return err
	}
	if executable != expected.Executable {
		return Fail(4, "handoff", "Activated executable path is not canonical")
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
	environment := []string{"HOME=" + c.Home, "PATH=" + strings.Join(search, string(os.PathListSeparator)), "WORKBENCH_CONFIG_DIR=" + c.Paths.Config, "WORKBENCH_DATA_DIR=" + c.Paths.Data, "WORKBENCH_STATE_DIR=" + c.Paths.State, "WORKBENCH_CACHE_DIR=" + c.Paths.Cache, "WORKBENCH_BIN_DIR=" + c.Paths.Bin}
	for _, name := range []string{"TERM", "LANG", "LC_ALL", "WSL_INTEROP", "WSL_DISTRO_NAME", "WSLENV"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	if err = syscall.Exec(executable, append([]string{executable}, args...), environment); err != nil {
		return Fail(1, "handoff", "Verified runtime could not start; installed and staged runtime files remain available; retry the selected command")
	}
	return nil
}
