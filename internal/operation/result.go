// Package operation owns shared context, execution and private operation state.
package operation

import (
	"context"
	"errors"
	"fmt"
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

// Error messages must be actionable and safe for public output.
type Error struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Code     Exit   `json:"-"`
}

func (e *Error) Error() string { return e.Message }

// Fail returns an [Error] with its exit status.
func Fail(code Exit, category, message string) error { return &Error{category, message, code} }

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

// Status is the outcome of a command or one of its components.
type Status string

// Statuses.
const (
	StatusComplete    Status = "complete"
	StatusUnchanged   Status = "unchanged"
	StatusSkipped     Status = "skipped"
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

// Component is one named part of a command's outcome.
type Component struct {
	Name string `json:"name"`
	// Title is the plain name the text view prints in place of Name, which
	// --json keeps for callers; empty prints Name.
	Title    string `json:"title,omitempty"`
	Status   Status `json:"status"`
	Message  string `json:"message,omitempty"`
	Recovery string `json:"recovery,omitempty"`
	Details  any    `json:"details,omitempty"`
}

// Result is the single envelope every command renders, as text or JSON.
type Result struct {
	SchemaVersion int         `json:"schema_version"`
	Command       string      `json:"command"`
	Status        Status      `json:"status"`
	Results       []Component `json:"results"`
	Warnings      []string    `json:"warnings"`
	Errors        []*Error    `json:"errors"`
	OperationID   string      `json:"operation_id,omitempty"`
	PlanDigest    string      `json:"plan_digest,omitempty"`
	// Summary is one line saying what a successful run achieved.
	Summary string `json:"summary,omitempty"`
}

// NewResult returns a complete, empty result for command.
func NewResult(command string) Result {
	return Result{
		SchemaVersion: 1,
		Command:       command,
		Status:        StatusComplete,
		Results:       []Component{},
		Warnings:      []string{},
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
		problem.Category, problem.Message = "interrupted", "Operation interrupted; inspect any recorded partial operation before retrying"
	}
	if known, ok := errors.AsType[*Error](err); ok {
		problem = known
	}
	r.Errors = append(r.Errors, problem)
	r.Status = statusFor(problem.Code)
}
