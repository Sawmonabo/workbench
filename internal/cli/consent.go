package cli

import (
	"context"
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
	fix := replan(cmd, c)
	consent := operation.Consent{
		ApprovedDigest: digest,
		NonInteractive: !o.interactive(),
		CompleteInputs: true,
		Replan:         fix,
	}
	if o.interactive() {
		consent.Confirm = func(
			ctx context.Context,
			plan operation.Plan,
			digest string,
		) (bool, error) {
			return o.confirmPlan(ctx, plan, digest, fix)
		}
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
// a fix line (see [workbenchFix]): the dry run that prints the plan and its
// digest, then the run that approves the plan by that digest, each with
// cmd's arguments and every flag it was given that picks the plan (all but
// unplannedFlags, and but one given its default).
func replan(cmd *cobra.Command, c operation.Context) []operation.Part {
	words := append(subcommand(cmd), cmd.Flags().Args()...)
	flags := givenFlags(cmd, unplannedFlags)
	return []operation.Part{
		workbenchFix(c, slices.Concat(words, []string{"--dry-run", "--json"}, flags)),
		workbenchFix(c, slices.Concat(words, []string{"--approve-plan", "DIGEST"}, flags)),
	}
}

// runAgain is cmd's run as one fix line, without what a refusal names: its
// command, then args (cmd's own, or nil for none), then every flag it was
// given but help and those in drop, as [replan] writes them.
func runAgain(
	cmd *cobra.Command,
	c operation.Context,
	args []string,
	drop ...string,
) operation.Part {
	words := append(subcommand(cmd), args...)
	return workbenchFix(c, append(words, givenFlags(cmd, append(drop, "help"))...))
}

// subcommand is the words of cmd's command after workbench: apply, project
// apply; none for workbench itself.
func subcommand(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	return strings.Fields(cmd.CommandPath())[1:]
}

// givenFlags are the flags cmd was given, as words to type, but those named
// in skip and one given its default.
func givenFlags(cmd *cobra.Command, skip []string) []string {
	var flags []string
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		switch values, many := flag.Value.(pflag.SliceValue); {
		case slices.Contains(skip, flag.Name), flag.Value.String() == flag.DefValue:
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
	return flags
}

// workbenchFix is the fix line that runs workbench with words: what to type
// to run it (see [operation.Context.WorkbenchCommand]), then each of words as
// a word a shell reads back, a path under c's home written from ~.
func workbenchFix(c operation.Context, words []string) operation.Part {
	quoted := []string{c.WorkbenchCommand()}
	for _, word := range words {
		quoted = append(quoted, c.ShellPath(word))
	}
	return operation.FixText(strings.Join(quoted, " "))
}

// pathsOnly is a context of Workbench's paths alone, resolved read-only, for
// a fix line made where no command's context is at hand (see
// [workbenchFix]); one that cannot be resolved still names workbench.
func pathsOnly() operation.Context {
	c, _ := operation.Resolve(operation.Options{ReadOnly: true})
	return c
}

// checkConsent is the approval rule apply and migrate share, checked
// before anything happens. --yes and --approve-plan both approve, so both
// together are an invalid invocation (exit 2), whose fix keeps the exact
// approval. A run nobody approved is refused (exit 3): it needs
// --approve-plan from a plan someone read, --yes from a person who trusts
// it, or a person at a terminal. A coding agent can type at a
// pseudo-terminal and --yes would apply a plan it never read, so it gets
// only --approve-plan. Both refusals give the run's new plan commands (see
// [replan]).
func checkConsent(cmd *cobra.Command, c operation.Context, o *options) error {
	switch {
	case o.yes && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"invocation",
			"--yes and --approve-plan both approve",
			runAgain(cmd, c, cmd.Flags().Args(), "yes"),
		)
	case operation.AgentSession() && (o.yes || o.approvePlan == ""):
		return agentRefused(replan(cmd, c)...)
	case o.yes || o.approvePlan != "", o.interactive() && hasTerminal():
		return nil
	}
	return operation.Fail(
		operation.ExitBlocked,
		"consent",
		"Nothing approved this plan",
		replan(cmd, c)...)
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
