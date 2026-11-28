// Package operation owns shared context, execution and private operation state.
package operation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Sawmonabo/workbench/internal/display"
)

// Exit is a process exit status. An [Error] carries one.
type Exit int

// Exit statuses.
const (
	ExitFailed      Exit = 1   // The operation failed.
	ExitInvalid     Exit = 2   // Input, configuration or recorded state is invalid.
	ExitBlocked     Exit = 3   // A prerequisite, approval or supported state is missing.
	ExitConflict    Exit = 4   // Inputs changed or disagree; review a new plan.
	ExitPartial     Exit = 5   // Some changes completed; recover from the checkpoint.
	ExitInterrupted Exit = 130 // A signal canceled the operation.
)

// Error is a refusal or a failure, safe for public output: its message is
// the headline, what is wrong with what, and its body the cause, when the
// headline does not say it, and what to type to fix it.
type Error struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Body
	Code Exit `json:"-"`
}

// Error is the headline and body in one line (see [Body.Sentence]).
func (e *Error) Error() string { return e.Sentence(e.Message) }

// Fail returns an [Error] with its exit status, its message the headline
// and parts its body:
//
//	operation.Fail(operation.ExitBlocked, "tool", display.Path(path)+" is locked",
//		operation.Cause("the flag uchg stops any file from replacing it"),
//		operation.FixPath(path, "chflags", "nouchg"))
func Fail(code Exit, category, message string, parts ...Part) error {
	return &Error{Category: category, Message: message, Body: Explain(parts...), Code: code}
}

// FailWith is [Fail] with a body already made, such as a [Component]'s.
func FailWith(code Exit, category, message string, body Body) error {
	return &Error{Category: category, Message: message, Body: body, Code: code}
}

// FailAs is err raised again with code and category: an [Error] keeps its
// headline and body, any other error is its text as the headline.
func FailAs(code Exit, category string, err error) error {
	headline, body := Explained(err)
	return &Error{Category: category, Message: headline, Body: body, Code: code}
}

// Explained is err's headline and body: an [Error]'s own, any other error's
// text and no body.
func Explained(err error) (string, Body) {
	if problem, ok := errors.AsType[*Error](err); ok {
		return problem.Message, problem.Body
	}
	return err.Error(), Body{}
}

// ExitCode returns the process exit status for err: 0 for nil,
// [ExitInterrupted] when canceled, the [Error] code when present, otherwise
// [ExitFailed].
func ExitCode(err error) Exit {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return ExitInterrupted
	}
	if problem, ok := errors.AsType[*Error](err); ok {
		return problem.Code
	}
	return ExitFailed
}

// Category is the category of the [Error] in err's chain, "" when there is
// none.
func Category(err error) string {
	if problem, ok := errors.AsType[*Error](err); ok {
		return problem.Category
	}
	return ""
}

// Status is the outcome of a command or one of its components.
type Status string

// Statuses.
const (
	StatusComplete    Status = "complete"
	StatusOK          Status = "ok" // a doctor check that found nothing wrong
	StatusUnchanged   Status = "unchanged"
	StatusSkipped     Status = "skipped"
	StatusPending     Status = "pending" // waits for a run, such as a migration's
	StatusAbsent      Status = "absent"
	StatusFailed      Status = "failed"
	StatusBlocked     Status = "blocked"
	StatusConflict    Status = "conflict"
	StatusPartial     Status = "partial"
	StatusInterrupted Status = "interrupted"
)

// StatusOf returns the status that reports err.
func StatusOf(err error) Status { return statusFor(ExitCode(err)) }

func statusFor(code Exit) Status {
	switch code {
	case 0:
		return StatusComplete
	case ExitBlocked:
		return StatusBlocked
	case ExitConflict:
		return StatusConflict
	case ExitPartial:
		return StatusPartial
	case ExitInterrupted:
		return StatusInterrupted
	default:
		return StatusFailed
	}
}

// Annotate prefixes *err with what failed, unless it is nil, canceled or
// already an actionable [Error]. Defer it at a package entry point so a raw
// operating-system or decoding error says which step it came from:
//
//	defer operation.Annotate(&err, "stage release %s", name)
func Annotate(err *error, format string, args ...any) {
	var known *Error
	if *err == nil || errors.Is(*err, context.Canceled) || errors.As(*err, &known) {
		return
	}
	*err = fmt.Errorf(format+": %w", append(args, *err)...)
}

// FileError is err, which an operation on the file at path returned, as an
// error that names path once, first, as people read it (see
// [display.Path]: ~/.local/state/…/history.db),
// then the reason: an error of the os package names the path itself, after
// the operation (open, stat, read), so only its reason follows path, and the
// error still matches what that reason matches, such as [fs.ErrNotExist] or
// [fs.ErrPermission]. Only an error that says no more than the os package's
// is cut so: one that wraps it in words of its own is kept whole, with what
// it matches besides.
func FileError(path string, err error) error {
	return fmt.Errorf("%s: %w", display.Path(path), FileReason(path, err))
}

// FileReason is what [FileError] says after path: the reason alone, for a
// cause line under a headline that names path.
func FileReason(path string, err error) error {
	if problem, ok := errors.AsType[*fs.PathError](err); ok && problem.Path == path &&
		problem.Error() == err.Error() {
		return problem.Err
	}
	return err
}

// Unread is what a check that changes nothing says of a file or folder it
// would move or delete and cannot read: "can't read", the path from home (see
// [display.PathFrom]), then the reason alone, as an error of the os package
// names the path itself.
func Unread(home, path string, err error) string {
	if problem, ok := errors.AsType[*fs.PathError](err); ok {
		err = problem.Err
	}
	return "can't read " + display.PathFrom(home, path) + " (" + err.Error() + ")"
}

// Component is one named part of a command's outcome.
type Component struct {
	Name string `json:"name"`
	// Title is the plain name the text view prints in place of Name, which
	// --json keeps for callers; empty prints Name.
	Title  string `json:"title,omitempty"`
	Status Status `json:"status"`
	// Message is the line's headline and Body what stands under it.
	Message string `json:"message,omitempty"`
	Body
	Recovery string `json:"recovery,omitempty"`
	Details  any    `json:"details,omitempty"`
}

// Warning is a note that changes nothing, such as a check that could not
// run: a headline and its body.
type Warning struct {
	Message string `json:"message"`
	Body
}

// Warn is the [Warning] of message, its headline, and parts, its body.
func Warn(message string, parts ...Part) Warning {
	return Warning{Message: message, Body: Explain(parts...)}
}

// WarnAll is a headline-only [Warning] of each message.
func WarnAll(messages ...string) []Warning {
	warnings := make([]Warning, len(messages))
	for i, message := range messages {
		warnings[i] = Warning{Message: message}
	}
	return warnings
}

// Result is the single envelope every command renders, as text or JSON.
type Result struct {
	SchemaVersion int         `json:"schema_version"`
	Command       string      `json:"command"`
	Status        Status      `json:"status"`
	Results       []Component `json:"results"`
	Warnings      []Warning   `json:"warnings"`
	Errors        []*Error    `json:"errors"`
	OperationID   string      `json:"operation_id,omitempty"`
	PlanDigest    string      `json:"plan_digest,omitempty"`
	// Summary is one clause saying what a successful run achieved, and Body
	// what stands under it: a line for a fact the clause leaves out, then the
	// next lines, what to type next.
	Summary string `json:"summary,omitempty"`
	Body
}

// NewResult returns a complete, empty result for command.
func NewResult(command string) Result {
	return Result{
		SchemaVersion: 1,
		Command:       command,
		Status:        StatusComplete,
		Results:       []Component{},
		Warnings:      []Warning{},
		Errors:        []*Error{},
	}
}

// SetError records err and sets the matching status. An [Error] keeps its
// message; any other error is reported with its cause.
func (r *Result) SetError(err error) {
	if err == nil {
		return
	}
	problem := &Error{
		Category: "execution",
		Message:  "Operation failed: " + err.Error(),
		Code:     ExitCode(err),
	}
	if problem.Code == ExitInterrupted {
		problem.Category, problem.Message = "interrupted", "Operation interrupted"
		problem.Body = Explain(
			FixText("check any partial operation it recorded before running it again"),
		)
	}
	if known, ok := errors.AsType[*Error](err); ok {
		problem = known
	}
	r.Errors = append(r.Errors, problem)
	r.Status = statusFor(problem.Code)
}
