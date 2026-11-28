package operation

import (
	"io"
	"os"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// ReadHookInput reads the JSON a Claude Code or Codex hook gets on stdin: at
// most 1 MiB, giving up after two seconds so a writer that never closes
// stdin cannot hold the hook, while a busy agent still has time to write it.
// A nil stdin, a terminal or a timeout gives nil.
func ReadHookInput(stdin *os.File) []byte {
	if stdin == nil || platform.IsTerminal(stdin.Fd()) {
		return nil
	}
	read := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
		read <- data
	}()
	select {
	case data := <-read:
		return data
	case <-time.After(2 * time.Second):
		return nil
	}
}
