package costs

import (
	"cmp"
	"context"
	"os"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// StatusInfo is what `workbench agent costs status` shows.
type StatusInfo struct {
	Ledger            string       `json:"ledger"`
	LedgerBytes       int64        `json:"ledger_bytes"`
	LastIngestAt      string       `json:"last_ingest_at"`
	LastIngestSummary string       `json:"last_ingest_summary"`
	LastError         string       `json:"last_error"`
	WorkerRunning     bool         `json:"worker_running"` // a worker holds the lock
	Tools             []ToolStatus `json:"tools"`
}

// SignInStatus is who a tool is signed in as now. Account is "unknown" when
// nobody is; Subscription is its id ("unknown" when none) and Label what the
// report shows for it.
type SignInStatus struct {
	Account      string `json:"account"`
	Subscription string `json:"subscription"`
	Label        string `json:"subscription_label"`
}

// Display writes the sign-in as the report names an account, "you@example.com · Max".
func (s SignInStatus) Display() string {
	return AccountName(s.Account, planName(s.Subscription, s.Label))
}

// ToolStatus is one tool's coverage, sign-in and hooks.
type ToolStatus struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Implemented bool              `json:"implemented"`
	Coverage    Coverage          `json:"coverage"`
	SignIn      SignInStatus      `json:"sign_in"`
	Hooks       map[string]string `json:"hooks,omitempty"`         // event → a Hook* state
	ConfigMove  *toolconfig.Move  `json:"config_move,omitempty"`   // nil unless a variable moved the tool's folder
	Skipped     int               `json:"skipped_files,omitempty"` // transcripts the source cannot read (compressed)
}

// Status reads the open ledger (see [Paths.Open]) and the live hook settings;
// it ingests and fetches nothing itself, and makes nothing. A file it can't
// read counts as missing, so when another process moved the costs folders
// while it read them (see [Paths.moved]) it reads them again where they are
// then.
func Status(ctx context.Context, ledger *Ledger) (StatusInfo, error) {
	paths, err := Locations()
	if err != nil {
		return StatusInfo{}, err
	}
	info, err := status(ctx, ledger, paths)
	if paths.moved() {
		if paths, err = Locations(); err == nil {
			info, err = status(ctx, ledger, paths)
		}
	}
	return info, err
}

func status(ctx context.Context, ledger *Ledger, paths Paths) (StatusInfo, error) {
	info := StatusInfo{Ledger: paths.Ledger}
	if stat, err := os.Stat(paths.Ledger); err == nil {
		info.LedgerBytes = stat.Size()
	}
	info.LastIngestAt = "never"
	if v, _ := ledger.Meta("last_ingest_at"); v != "" {
		info.LastIngestAt = v
	}
	info.LastIngestSummary, _ = ledger.Meta("last_ingest_summary")
	info.LastError, _ = ledger.Meta("last_error")
	info.WorkerRunning = operation.IsLockHeld(paths.lock())
	for _, tool := range Tools {
		status := ToolStatus{Name: tool.Name, Title: tool.Title, Implemented: tool.Source != nil}
		if tool.Source != nil {
			official, err := ledger.officialRates(ctx, tool.Name)
			if err != nil {
				return info, err
			}
			var fetched time.Time
			if official.FetchedAt > 0 {
				fetched = fromEpoch(official.FetchedAt)
			}
			if status.Coverage, err = coverage(ctx, ledger, fetched, tool.Name); err != nil {
				return info, err
			}
			signIn := tool.Source.SignIn(paths.Home)
			status.SignIn = SignInStatus{
				Account:      cmp.Or(signIn.Account, "unknown"),
				Subscription: cmp.Or(signIn.Subscription, "unknown"),
				Label:        subscriptionLabel(signIn.Subscription, signIn.Label),
			}
			status.Hooks = map[string]string{}
			for i, state := range tool.Hooks(paths.Home, IngestHooks) {
				status.Hooks[IngestHooks[i].Event] = state
			}
			if tool.Move != nil {
				if move, moved := tool.Move(paths.Home); moved {
					status.ConfigMove = &move
				}
			}
			if counter, ok := tool.Source.(skipCounter); ok {
				status.Skipped = counter.Skipped(paths.Home)
			}
		}
		info.Tools = append(info.Tools, status)
	}
	return info, nil
}
