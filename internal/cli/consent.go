package cli

import (
	"io"
	"os"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// consentFor approves a plan by digest when unattended, otherwise by a Yes at
// the terminal. Callers with incomplete inputs clear CompleteInputs.
func consentFor(o *options, digest string) operation.Consent {
	consent := operation.Consent{
		ApprovedDigest: digest,
		NonInteractive: !o.interactive(),
		CompleteInputs: true,
	}
	if o.interactive() {
		consent.Confirm = confirmPlan
	}
	return consent
}

// checkConsent is the approval rule apply and migrate share, checked
// before anything happens. --yes and --approve-plan both approve, so both
// together are an invalid invocation (exit 2). A run nobody approved is
// refused (exit 3): it needs --approve-plan from a plan someone read, --yes
// from a person who trusts it, or a person at a terminal. A coding agent can
// type at a pseudo-terminal and --yes would apply a plan it never read, so it
// gets only --approve-plan. The refusal names command's dry run and its
// approval with the flags that pick the plan, which both must repeat.
func checkConsent(o *options, command string) error {
	switch {
	case o.yes && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"invocation",
			"--yes and --approve-plan both approve",
			operation.FixText("drop --yes or --approve-plan"),
		)
	case operation.AgentSession() && (o.yes || o.approvePlan == ""):
		return agentRefused()
	case o.yes || o.approvePlan != "", o.interactive() && hasTerminal():
		return nil
	}
	dryRun := []string{"workbench", command, "--dry-run", "--json"}
	approve := []string{"workbench", command, "--approve-plan", "DIGEST"}
	if o.localBuild {
		dryRun, approve = append(dryRun, "--local-build"), append(approve, "--local-build")
	}
	fix := func(words []string) operation.Part {
		if o.resolve.Destination == "" {
			return operation.Fix(words...)
		}
		return operation.FixPath(o.resolve.Destination, append(words, "--destination")...)
	}
	return operation.Fail(
		operation.ExitBlocked,
		"consent",
		"Nothing approved this plan",
		fix(dryRun),
		fix(approve),
	)
}

// nativeConsole gives interactive native runs the controlling terminal so
// installers can prompt, and an apply at it on a Mac asks for the Mac password
// once there, up front or when Homebrew (or the touch-id-sudo step) first needs
// it, or leaves the asking to sudo where it uses Touch ID (see
// [operation.WithAdmin]). Unattended runs stream redacted diagnostics. A coding
// agent never gets the terminal: it cannot answer the machine questions, and its
// native output streams instead (sudo still opens /dev/tty itself, so an approved
// agent apply is unchanged: it is never asked by Workbench and gets no helper).
func nativeConsole(o *options, diagnostics io.Writer) (*os.File, io.Writer, func()) {
	if o.interactive() && !operation.AgentSession() {
		if terminal := operation.StandardTerminal(); terminal != nil {
			return terminal, nil, func() { _ = terminal.Close() }
		}
	}
	return nil, diagnostics, func() {}
}
