package release

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// PrivateTree rejects redirected, borrowed or permissive private trees: no
// folder from target up may be a link or a file, and each one within root
// must be this user's with mode 0700. With create it then makes the missing
// folders; without, a missing one fails. It never repairs modes or follows
// links while creating a selected descendant. Unlike
// [operation.PrivateDirectory] it checks every level and can check without
// creating.
func PrivateTree(root, target string, create bool) error {
	if !filepath.IsAbs(root) || !operation.Within(root, target) {
		return operation.Fail(
			operation.ExitInvalid,
			"private_path",
			"Private target escapes the selected runtime directory",
		)
	}
	for current := target; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return operation.Fail(
					operation.ExitInvalid,
					"private_path",
					"Private directory ancestry contains a link or non-directory",
				)
			}
			if operation.Within(root, current) {
				stat, ok := info.Sys().(*syscall.Stat_t)
				if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o700 {
					return operation.Fail(
						operation.ExitInvalid,
						"permissions",
						"Private runtime directories must be owned by this user with mode 0700",
					)
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		} else if !create {
			return operation.Fail(
				operation.ExitBlocked,
				"private_path",
				"Required private runtime directory is absent",
			)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	if create {
		if err := operation.MakeParents(root); err != nil {
			return err
		}
		return os.MkdirAll(target, 0o700)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
