// Package migrate holds Workbench's numbered, idempotent migrations: moves of
// files Workbench itself wrote outside its runtime folders into them, and
// deletes of the caches, logs, locks and empty files it wrote there and of an
// old copy whose new copy holds the same bytes (ADR 0008). A move of another
// package's files is done by that package; this package holds the list, the
// check and run loop, and the run record.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/costs"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// Status is where one migration stands.
type Status int

// The statuses. A held lock or an open ledger is no status: Run says so and
// fails, and the next apply tries again.
const (
	Done       Status = iota // no old file the migration knows remains
	Pending                  // an old file exists and its new path does not, or a path can't be read
	Unfinished               // a stopped run left its marks
	Conflict                 // old and new data both exist, or old data can't be found
)

func (s Status) String() string {
	return [...]string{"done", "pending", "unfinished", "conflict"}[s]
}

// Locations holds both sets of paths: the new ones from operation.Resolve and
// the old ones as the old code resolved them. A relative old value can't be
// found again; its location is "" and Notes says so.
type Locations struct {
	Home     string
	Runtime  operation.Paths
	Costs    costs.Paths
	OldCosts costs.OldPaths
	// OldCache is ${XDG_CACHE_HOME:-$HOME/.cache}, where the old startup files
	// saved the completion script; OldScriptCache is
	// ${CLAUDE_COSTS_CACHE:-$HOME/.cache/claude-costs}, the old script's cache.
	OldCache, OldScriptCache string
	Notes                    []string
}

// Migration is one numbered migration. Check changes nothing; its text says
// what a run would do, or, for a Done migration, what it found and leaves. A
// path it can't inspect counts as there, so nothing is called done that
// isn't, and the run then says why. Run finishes whatever is left, and
// running it twice only completes the remaining moves. Run returns the files,
// home-relative, it found and left because Workbench didn't write them.
type Migration struct {
	ID       string // "0001-costs-folders"; names it in the plan and output
	Title    string // "Move the costs files into Workbench's folders"
	Doing    string // "moving the costs files into Workbench's folders"
	What     string // one paragraph naming every path it touches
	Recovery string // how to undo it, for the plan
	Check    func(Locations) (Status, string)
	Run      func(context.Context, Locations) ([]string, error)
}

// Registry lists every migration in number order. A released migration is
// never renumbered or removed; adding one is a numbered file and a line here.
var Registry = []Migration{costsFolders, completionCache, oldCostsCache}

// OldLocationVariables are the variables the old locations were read from.
// The apply passes each one that is set to its scripts as
// WORKBENCH_OLD_<NAME>, and home/.chezmoitemplates/old-locations.sh hands it
// back under its own name to the installed workbench only; keep both lists
// the same.
var OldLocationVariables = []string{
	"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	"CLAUDE_COSTS_LEDGER", "CLAUDE_COSTS_STATE", "CLAUDE_COSTS_RATES", "CLAUDE_COSTS_CACHE",
}

// IDs are the registry's IDs, which the apply passes to the installed binary
// as WORKBENCH_MIGRATIONS so it can say which ones it doesn't know.
func IDs() []string {
	ids := make([]string, len(Registry))
	for i, m := range Registry {
		ids[i] = m.ID
	}
	return ids
}

// Locate resolves both sets of paths. It reads paths only.
func Locate() (Locations, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return Locations{}, err
	}
	l := Locations{
		Home:     c.Home,
		Runtime:  c.Paths,
		Costs:    costs.LocationsIn(c.Home, c.Paths),
		OldCosts: costs.OldLocations(c.Home),
	}
	l.Notes = append(l.Notes, l.OldCosts.Notes...)
	l.OldCache = l.shellDefault("XDG_CACHE_HOME", filepath.Join(c.Home, ".cache"))
	l.OldScriptCache = l.shellDefault(
		"CLAUDE_COSTS_CACHE",
		filepath.Join(c.Home, ".cache", "claude-costs"),
	)
	return l, nil
}

// shellDefault is [costs.ShellDefault], noting a relative value.
func (l *Locations) shellDefault(name, fallback string) string {
	path, relative := costs.ShellDefault(l.Home, name, fallback)
	if relative != "" {
		l.Notes = append(l.Notes, relative+", so the files it named can't be found")
	}
	return path
}

// Finding is one migration with what its Check found.
type Finding struct {
	Migration
	Status Status
	Detail string
}

// Check runs every migration's Check, in number order.
func Check(l Locations) []Finding {
	findings := make([]Finding, len(Registry))
	for i, m := range Registry {
		status, detail := m.Check(l)
		findings[i] = Finding{m, status, detail}
	}
	return findings
}

func due(f Finding) bool { return f.Status == Pending || f.Status == Unfinished }

// Run runs every migration with something to do, in number order, and says
// what it does through say, one line at a time: "checking", then for each
// migration "<ID>: <Doing>" and "<ID>: done", or "nothing to migrate". It
// checks first, and takes no lock when nothing has anything to do. Otherwise
// it holds [operation.Paths.MigrateLock], so one run moves files at a time,
// and checks again under it. A migration that can't finish says why and the
// others still run; the error then names those that didn't finish. wanted
// names the migrations the caller expects (see [Probe]); one this binary
// doesn't know is named, and the run then fails as blocked.
func Run(ctx context.Context, l Locations, wanted []string, say func(string)) error {
	say("checking")
	newer := Unknown(wanted)
	for _, id := range newer {
		say(id + ": " + needsNewer)
	}
	findings := Check(l)
	if slices.ContainsFunc(findings, due) {
		release, held, err := operation.TryLock(l.Runtime.MigrateLock())
		if err != nil {
			return operation.Fail(
				operation.ExitBlocked,
				"busy",
				"The migration lock can't be taken ("+err.Error()+"), and the next apply tries again",
			)
		}
		if held {
			failed := conflicts(findings, say)
			say("another migration run is moving files, and the next apply tries again")
			if len(failed) > 0 {
				return unfinished(failed, operation.ExitConflict)
			}
			return operation.Fail(
				operation.ExitBlocked,
				"busy",
				"Another migration run holds the lock",
			)
		}
		defer release()
		findings = Check(l)
	}
	failed := conflicts(findings, say)
	exit := operation.ExitBlocked
	if len(failed) > 0 {
		exit = operation.ExitConflict
	}
	if !slices.ContainsFunc(findings, due) && len(failed) == 0 && len(newer) == 0 {
		say("nothing to migrate")
		return nil
	}
	for _, f := range findings {
		if !due(f) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runOne(ctx, l, f, say); err != nil {
			failed = append(failed, f.ID)
			if operation.ExitCode(err) == operation.ExitConflict {
				exit = operation.ExitConflict
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case len(failed) > 0:
		return unfinished(failed, exit)
	case len(newer) > 0:
		return operation.Fail(operation.ExitBlocked, "migrate",
			strings.Join(newer, ", ")+" "+needsNewer+", so run workbench update and apply again")
	}
	return nil
}

// conflicts says what each migration in conflict leaves, and returns their IDs.
func conflicts(findings []Finding, say func(string)) []string {
	var ids []string
	for _, f := range findings {
		if f.Status == Conflict {
			say(f.ID + ": " + f.Detail)
			ids = append(ids, f.ID)
		}
	}
	return ids
}

// runOne runs one migration and says how it ended; the error is why it didn't
// finish.
func runOne(ctx context.Context, l Locations, f Finding, say func(string)) error {
	say(f.ID + ": " + f.Doing)
	left, err := f.Run(ctx, l)
	if len(left) > 0 {
		say(f.ID + ": " + keeps(left))
	}
	if err != nil {
		say(f.ID + ": " + strings.Join(strings.Fields(err.Error()), " "))
		return err
	}
	say(f.ID + ": done")
	if err = markDone(l, f.ID); err != nil {
		say(f.ID + ": the migration record wasn't saved: " + err.Error())
	}
	return nil
}

// unfinished is the error of a run some migrations didn't finish. A conflict
// waits for the owner, who must deal with each file the output named as it
// says; anything else the next apply tries again.
func unfinished(ids []string, exit operation.Exit) error {
	names := strings.Join(ids, ", ")
	if exit == operation.ExitConflict {
		return operation.Fail(exit, "conflict", names+
			" can't finish until you deal with each file named above as it says")
	}
	return operation.Fail(exit, "migrate", names+
		" didn't finish (the output above says why), and the next apply tries again")
}

// needsNewer is what is said of a migration the installed binary doesn't know.
const needsNewer = "needs a newer installed workbench"

// Unknown are the IDs in wanted that this binary's registry doesn't hold.
func Unknown(wanted []string) []string {
	return slices.DeleteFunc(slices.Clone(wanted), func(id string) bool {
		return slices.Contains(IDs(), id)
	})
}

// keeps is the text naming the files a migration leaves because Workbench
// didn't write them, "" when there are none.
func keeps(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "keeps " + names[0] + " (Workbench didn't write it)"
	}
	return "keeps " + strings.Join(names, ", ") + " (Workbench didn't write them)"
}

// Probe is what `workbench migrate --probe` prints for the apply's plan: one
// "migrations: <ID>: <paths>" line per migration with something to do,
// "migrations: ? …" for one that conflicts or that this binary doesn't know
// (wanted names the planning binary's migrations, from WORKBENCH_MIGRATIONS),
// or the one line "migrations: = none to run". No line holds "; ", which the
// plan uses to join one step's lines.
func Probe(l Locations, wanted []string) []string {
	var lines []string
	for _, f := range Check(l) {
		switch f.Status {
		case Pending, Unfinished:
			lines = append(lines, "migrations: "+f.ID+": "+f.Detail)
		case Conflict:
			lines = append(lines, "migrations: ? "+f.ID+": "+f.Detail)
		default:
		}
	}
	for _, id := range Unknown(wanted) {
		lines = append(lines, "migrations: ? "+id+" "+needsNewer+" (workbench update)")
	}
	if len(lines) == 0 {
		return []string{"migrations: = none to run"}
	}
	return lines
}

// Plan is what `workbench migrate --apply` asks approval for: one fixed effect
// per migration with something to do, named by its ID and described by its
// paths, so the digest changes when the paths do. A migration in conflict is
// a warning.
func Plan(scope operation.Scope, findings []Finding) operation.Plan {
	plan := operation.Plan{
		Scope:         scope,
		Edits:         []operation.Edit{},
		Prerequisites: []string{},
		Effects:       []operation.Effect{},
		RecoveryLimits: []string{
			"Migrations are not checkpointed; their moves keep every ledger row and every other file's bytes, and they delete only " +
				"caches, logs, locks and empty files Workbench wrote, and old copies whose new " +
				"copy holds the same bytes",
		},
		Complete: true,
	}
	for _, f := range findings {
		switch {
		case due(f):
			plan.Effects = append(plan.Effects, operation.Effect{
				Name:        f.ID,
				Description: f.Detail,
				Privilege:   "user",
				Recovery:    f.Recovery,
				Title:       f.Title,
				Summary:     f.Detail,
				What:        f.What,
				RunsAs:      "you",
				Fixed:       true,
				Checked:     true,
			})
		case f.Status == Conflict:
			plan.Warnings = append(plan.Warnings, f.ID+": "+f.Detail)
		}
	}
	return plan
}

// record is migrations.json in Workbench's state folder: when each migration
// reached Done in a `workbench migrate` or apply run. Only `workbench migrate`
// reads it, to say when; correctness never depends on it.
type record struct {
	SchemaVersion int               `json:"schema_version"`
	Done          map[string]string `json:"done"`
}

func recordPath(l Locations) string { return filepath.Join(l.Runtime.State, "migrations.json") }

// readRecord reads the record; a missing one is empty. One it can't read, or
// of a schema version other than 1, is empty too, with an error, so a run
// never replaces it.
func readRecord(l Locations) (record, error) {
	empty := record{SchemaVersion: 1, Done: map[string]string{}}
	if _, err := os.Lstat(recordPath(l)); errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	data, err := operation.ReadPrivateInput(recordPath(l), 1<<20)
	if err != nil {
		return empty, err
	}
	var r record
	if json.Unmarshal(data, &r) != nil || r.SchemaVersion != 1 || r.Done == nil {
		return empty, operation.Fail(
			operation.ExitInvalid,
			"record",
			recordPath(l)+" is not a schema 1 migration record; it was not changed",
		)
	}
	return r, nil
}

// DoneAt is when the record says id reached Done, in this machine's time
// ("Oct 14 at 6:33 PM"), "" when it doesn't say.
func DoneAt(l Locations, id string) string {
	r, _ := readRecord(l)
	at, err := time.Parse(time.RFC3339, r.Done[id])
	if err != nil {
		return ""
	}
	return at.Local().Format("Jan 2 at 3:04 PM")
}

func markDone(l Locations, id string) error {
	r, err := readRecord(l)
	if err != nil {
		return err
	}
	r.Done[id] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if _, err = operation.PrivateDirectory(l.Runtime.State, ""); err != nil {
		return err
	}
	return operation.ReplacePrivateFile(recordPath(l), append(data, '\n'))
}
