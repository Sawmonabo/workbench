package operation

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
