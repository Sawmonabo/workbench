package cli

import (
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// consentFor approves a plan by digest when unattended, otherwise by a Yes at
// the terminal, with cmd's new plan commands (see [replan]) for a refusal.
// Callers with incomplete inputs clear CompleteInputs.
func consentFor(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	digest string,
) operation.Consent {
	consent := operation.Consent{
		ApprovedDigest: digest,
		NonInteractive: !o.interactive(),
		CompleteInputs: true,
		Replan:         replan(cmd, c),
	}
	if o.interactive() {
		consent.Confirm = confirmPlan
	}
	return consent
}

// unplannedFlags are the flags that pick no part of a plan: they approve it,
// ask about it or choose how it is shown.
var unplannedFlags = []string{
	"approve-plan",
	"yes",
	"choose",
	"reset",
	"dry-run",
	"json",
	"non-interactive",
	"verbose",
	"help",
}

// replan is the commands that make and approve a new plan of cmd's run, each
// a fix line: the dry run that prints the plan and its digest, then the run
// that approves the plan by that digest, each with cmd's arguments and every
// flag it was given that picks the plan (all but unplannedFlags, and but one
// given its default), words a shell reads back, a path under c's home
// written from ~.
func replan(cmd *cobra.Command, c operation.Context) []operation.Part {
	words := append(strings.Fields(cmd.CommandPath()), cmd.Flags().Args()...)
	var flags []string
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		switch values, many := flag.Value.(pflag.SliceValue); {
		case slices.Contains(unplannedFlags, flag.Name), flag.Value.String() == flag.DefValue:
		case many:
			for _, value := range values.GetSlice() {
				flags = append(flags, "--"+flag.Name, value)
			}
		case flag.Value.Type() == "bool" && flag.Value.String() == "true":
			flags = append(flags, "--"+flag.Name)
		case flag.Value.Type() == "bool":
			flags = append(flags, "--"+flag.Name+"="+flag.Value.String())
		default:
			flags = append(flags, "--"+flag.Name, flag.Value.String())
		}
	})
	command := func(approval ...string) operation.Part {
		all := slices.Concat(words, approval, flags)
		for i, word := range all {
			all[i] = c.ShellPath(word)
		}
		return operation.FixText(strings.Join(all, " "))
	}
	return []operation.Part{command("--dry-run", "--json"), command("--approve-plan", "DIGEST")}
}

// checkConsent is the approval rule apply and migrate share, checked
// before anything happens. --yes and --approve-plan both approve, so both
// together are an invalid invocation (exit 2). A run nobody approved is
// refused (exit 3): it needs --approve-plan from a plan someone read, --yes
// from a person who trusts it, or a person at a terminal. A coding agent can
// type at a pseudo-terminal and --yes would apply a plan it never read, so it
// gets only --approve-plan. Both refusals give replan, the run's new plan
// commands (see [replan]).
func checkConsent(o *options, replan []operation.Part) error {
	switch {
	case o.yes && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"invocation",
			"--yes and --approve-plan both approve",
			operation.FixText("drop --yes or --approve-plan"),
		)
	case operation.AgentSession() && (o.yes || o.approvePlan == ""):
		return operation.Fail(operation.ExitBlocked, "consent", "An agent runs this", replan...)
	case o.yes || o.approvePlan != "", o.interactive() && hasTerminal():
		return nil
	}
	return operation.Fail(operation.ExitBlocked, "consent", "Nothing approved this plan", replan...)
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
