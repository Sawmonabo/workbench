package toolconfig

import (
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// ClaudeManagedSettings are the machine's managed settings files, in the
// order Claude Code merges them, a later one overriding an earlier:
// managed-settings.json, then each managed-settings.d/*.json in name order.
func ClaudeManagedSettings() []string {
	managed := platform.ClaudeManagedFolder
	files := []string{filepath.Join(managed, "managed-settings.json")}
	drops, _ := filepath.Glob(filepath.Join(managed, "managed-settings.d", "*.json"))
	for _, drop := range drops {
		if !strings.HasPrefix(filepath.Base(drop), ".") {
			files = append(files, drop)
		}
	}
	return files
}
