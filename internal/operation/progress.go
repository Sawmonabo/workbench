package operation

import "time"

// Progress shows what a long operation is doing while nothing else uses the
// terminal. The CLI supplies it: a live status line at a terminal, plain lines
// on stderr otherwise.
type Progress interface {
	// Start shows title and the time since, with each later Step, until Stop.
	Start(title string)
	// Step names what the operation is doing now.
	Step(step string)
	// Stop removes the status line.
	Stop()
}

// ShowProgress shows title and each later [Context.Step] until the returned
// function is called, which callers do before a prompt or before handing the
// terminal to another program.
func (c Context) ShowProgress(title string) (stop func()) {
	if c.Progress == nil {
		return func() {}
	}
	c.Progress.Start(title)
	return c.Progress.Stop
}

// Step names what the operation is doing now, while progress is shown.
func (c Context) Step(step string) {
	if c.Progress != nil {
		c.Progress.Step(step)
	}
}

// ToolState is where one of Workbench's own tools stands during an update.
type ToolState int

const (
	// ToolDownloading is a tool whose file is being fetched.
	ToolDownloading ToolState = iota
	// ToolInstalling is a tool being installed without byte counts (uv
	// installs Python with its own progress off).
	ToolInstalling
	// ToolReady is a tool in place, reused or just installed.
	ToolReady
	// ToolFailed is a tool that failed the final check of the tools in place.
	ToolFailed
)

// ToolProgress is a Progress that also shows each tool and each download.
// Only the update view has it; other progress ignores these events.
type ToolProgress interface {
	// Tool reports name's state. version is the tool's version; previous,
	// when set, the version of the previous private copy it replaces.
	Tool(name string, state ToolState, version, previous string)
	// Transfer reports done of total bytes of name's download; total is -1
	// when the server sends no size.
	Transfer(name string, done, total int64)
}

// Tool reports a tool's state to a progress that shows tools.
func (c Context) Tool(name string, state ToolState, version, previous string) {
	if progress, ok := c.Progress.(ToolProgress); ok {
		progress.Tool(name, state, version, previous)
	}
}

// transferInterval is how often a download's byte count is reported.
const transferInterval = 50 * time.Millisecond

// Transfer returns the byte counter for name's download, or nil when the
// progress shows no downloads. The counter reports at most every
// transferInterval, and always the last count.
func (c Context) Transfer(name string) func(done, total int64) {
	progress, ok := c.Progress.(ToolProgress)
	if !ok {
		return nil
	}
	var last time.Time
	return func(done, total int64) {
		if now := time.Now(); done == total || now.Sub(last) >= transferInterval {
			last = now
			progress.Transfer(name, done, total)
		}
	}
}
