// Package operation owns shared context, execution and private operation state.
package operation

import (
	"context"
	"errors"
)

// Error messages must be actionable and safe for public output.
type Error struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Code     int    `json:"-"`
}

func (e *Error) Error() string                      { return e.Message }
func Fail(code int, category, message string) error { return &Error{category, message, code} }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var problem *Error
	if errors.As(err, &problem) {
		return problem.Code
	}
	return 1
}

type Component struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Recovery string `json:"recovery,omitempty"`
	Details  any    `json:"details,omitempty"`
}

type Result struct {
	SchemaVersion int         `json:"schema_version"`
	Command       string      `json:"command"`
	Status        string      `json:"status"`
	Results       []Component `json:"results"`
	Warnings      []string    `json:"warnings"`
	Errors        []*Error    `json:"errors"`
	OperationID   string      `json:"operation_id,omitempty"`
	PlanDigest    string      `json:"plan_digest,omitempty"`
}

func NewResult(command string) Result {
	return Result{
		SchemaVersion: 1,
		Command:       command,
		Status:        "complete",
		Results:       []Component{},
		Warnings:      []string{},
		Errors:        []*Error{},
	}
}

func (r *Result) SetError(err error) {
	if err == nil {
		return
	}
	problem := &Error{
		Category: "execution",
		Message:  "Operation failed: " + err.Error(),
		Code:     ExitCode(err),
	}
	if problem.Code == 130 {
		problem.Category, problem.Message = "interrupted", "Operation interrupted; inspect any recorded partial operation before retrying"
	}
	var known *Error
	if errors.As(err, &known) {
		problem = known
	}
	r.Errors = append(r.Errors, problem)
	r.Status = "failed"
	switch problem.Code {
	case 3:
		r.Status = "blocked"
	case 4:
		r.Status = "conflict"
	case 5:
		r.Status = "partial"
	case 130:
		r.Status = "interrupted"
	}
}
