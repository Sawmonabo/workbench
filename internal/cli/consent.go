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

// nativeConsole gives interactive native runs the controlling terminal so sudo
// and installers can prompt. Unattended runs stream redacted diagnostics.
func nativeConsole(o *options, diagnostics io.Writer) (*os.File, io.Writer, func()) {
	if o.interactive() {
		if terminal := operation.StandardTerminal(); terminal != nil {
			return terminal, nil, func() { _ = terminal.Close() }
		}
	}
	return nil, diagnostics, func() {}
}
