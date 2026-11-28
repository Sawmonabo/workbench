package costs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

const (
	ratesMaxAge  = 7 * 24 * time.Hour // refetch the official card after this
	ratesRetry   = 24 * time.Hour     // but never try more often than this
	fetchTimeout = 5 * time.Second
	pageLimit    = 16 << 20 // the largest price page read; a longer one fails
)

// officialFile is one tool's official price list: its rows by model prefix
// and rate field, the page it came from, when it was downloaded and when the
// page was last asked (seconds since the epoch), and the page's validators.
// The ledger keeps it in prices and price_lists. Its JSON keys are those of
// the list file beside a ledger of schema version 5, which the upgrade reads
// (see fillPrices).
type officialFile struct {
	FetchedAt    float64                       `json:"fetched_at"`
	CheckedAt    float64                       `json:"checked_at"`
	ETag         string                        `json:"etag"`
	LastModified string                        `json:"last_modified"`
	URL          string                        `json:"url"`
	Rates        map[string]map[string]float64 `json:"rates"`
}

// rates are the list's rows that carry all five fields.
func (f officialFile) rates() RateCard {
	card := RateCard{}
	for model, fields := range f.Rates {
		var rate Rate
		complete := true
		for _, name := range rateFields {
			v, ok := fields[name]
			complete = complete && ok
			rate.set(name, v)
		}
		if complete {
			rate.Source = "official"
			card[model] = rate
		}
	}
	return card
}

// fields is the rate as a price list holds it, by rate field.
func (r Rate) fields() map[string]float64 {
	out := make(map[string]float64, len(rateFields))
	for _, name := range rateFields {
		out[name] = r.field(name)
	}
	return out
}

// officialPath is one tool's price list file beside the ledger at ledger,
// <tool>/rates-official.json in its folder, where a ledger of schema version 5
// had its downloaded prices: its upgrade reads the file (see fillPrices), and
// migration 0008 deletes it (see [PriceLists]).
func officialPath(ledger string, tool Tool) string {
	return filepath.Join(filepath.Dir(ledger), tool.Name, "rates-official.json")
}

// PriceLists are the price list files beside the ledger at ledger, one per
// recorded tool, whether they are there or not (see officialPath).
func PriceLists(ledger string) []string {
	var lists []string
	for _, tool := range Tools {
		if tool.Source != nil {
			lists = append(lists, officialPath(ledger, tool))
		}
	}
	return lists
}

// fillPrices reads each tool's price list file beside the ledger at ledger
// into prices and price_lists, in tx, the transaction of the ledger's upgrade.
// A file that is missing, is not a plain file (see
// [operation.OpenRegularFile]) or does not parse fills nothing for its tool,
// whose list the import then downloads. The read has no bound: the list was
// written from a page of up to 16 MiB, into the ledger's private folder. A
// list without one complete row still fills its price_lists row, which keeps
// when the page was last asked, so the import still asks at most once a day.
//
// The ledger's folder moving under agent/ while it reads (see
// [MoveAgentFolders]) leaves a file where the lists were, which would fill
// nothing for a list that is there: it then fails, which undoes the upgrade,
// and the open is tried again where the ledger is now (see [Paths.moved]).
func fillPrices(ctx context.Context, tx *sql.Tx, ledger string) error {
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		opened, err := operation.OpenRegularFile(officialPath(ledger, tool))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			continue
		}
		var file officialFile
		if json.Unmarshal(data, &file) != nil {
			continue
		}
		if err := storeOfficial(ctx, tx, tool.Name, file); err != nil {
			return err
		}
	}
	if folder := filepath.Dir(ledger); !operation.IsFolder(folder) {
		return operation.FileError(folder,
			fmt.Errorf("moved while its price lists were read: %w", syscall.ENOTDIR))
	}
	return nil
}

// officialRates is tool's official price list as the ledger keeps it: the
// zero list, with no rows, when it keeps none.
func (l *Ledger) officialRates(ctx context.Context, tool string) (officialFile, error) {
	file := officialFile{Rates: map[string]map[string]float64{}}
	err := l.db.QueryRowContext(
		ctx,
		"SELECT url, fetched_at, checked_at, etag, last_modified FROM price_lists WHERE tool = ?",
		tool,
	).Scan(&file.URL, &file.FetchedAt, &file.CheckedAt, &file.ETag, &file.LastModified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return file, err
	}
	rows, err := l.db.QueryContext(
		ctx,
		`SELECT prefix, input, output, cache_write_5m, cache_write_1h, cache_read
		   FROM prices WHERE tool = ?`,
		tool,
	)
	if err != nil {
		return file, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var prefix string
		var rate Rate
		if err := rows.Scan(
			&prefix,
			&rate.Input,
			&rate.Output,
			&rate.CacheWrite5m,
			&rate.CacheWrite1h,
			&rate.CacheRead,
		); err != nil {
			return file, err
		}
		file.Rates[prefix] = rate.fields()
	}
	return file, rows.Err()
}

// replaceOfficial makes file tool's official price list, in one short
// transaction: its complete rows replace the tool's prices, and its times and
// validators the tool's price_lists row.
func (l *Ledger) replaceOfficial(ctx context.Context, tool string, file officialFile) error {
	return sqlitefile.Transact(
		ctx,
		l.db,
		func(tx *sql.Tx) error { return storeOfficial(ctx, tx, tool, file) },
	)
}

// storeOfficial is [Ledger.replaceOfficial] in tx.
func storeOfficial(ctx context.Context, tx *sql.Tx, tool string, file officialFile) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM prices WHERE tool = ?", tool); err != nil {
		return err
	}
	for prefix, rate := range file.rates() {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO prices (tool, prefix, input, output, cache_write_5m, cache_write_1h, cache_read)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			tool,
			prefix,
			rate.Input,
			rate.Output,
			rate.CacheWrite5m,
			rate.CacheWrite1h,
			rate.CacheRead,
		); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO price_lists (tool, url, fetched_at, checked_at, etag, last_modified)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tool) DO UPDATE SET url = excluded.url, fetched_at = excluded.fetched_at,
		   checked_at = excluded.checked_at, etag = excluded.etag,
		   last_modified = excluded.last_modified`,
		tool, file.URL, file.FetchedAt, file.CheckedAt, file.ETag, file.LastModified,
	)
	return err
}

// recordAttempt records in tool's price_lists row that its page was asked at
// checked and, when fetched is not zero, found unchanged then. The tool's
// prices, and the rest of the row, stay as they are, whatever their age.
func (l *Ledger) recordAttempt(ctx context.Context, tool string, checked, fetched float64) error {
	_, err := l.db.ExecContext(
		ctx,
		`INSERT INTO price_lists (tool, url, fetched_at, checked_at, etag, last_modified)
		 VALUES (?, '', ?, ?, '', '')
		 ON CONFLICT(tool) DO UPDATE SET checked_at = excluded.checked_at,
		   fetched_at = max(fetched_at, excluded.fetched_at)`,
		tool, fetched, checked,
	)
	return err
}

// epoch is t as the price lists store a time: seconds since the epoch.
func epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// fromEpoch is the time of seconds since the epoch, as epoch stores it.
func fromEpoch(seconds float64) time.Time { return time.Unix(0, int64(seconds*1e9)) }

// ratesLabel names tool's price list in the rates notes: "rates" for the
// first tool, "rates <tool>" for every other.
func ratesLabel(tool string) string {
	if tool == firstTool {
		return "rates"
	}
	return "rates " + tool
}

// cachedText is the rates note of a price list kept as it is, age old.
func cachedText(label string, age time.Duration) string {
	return label + ": cached (" + display.Duration(age) + " old)"
}

// refreshOfficial is a conditional GET of the tool's pricing page (see
// [release.Get]), made with no transaction open, and then one short transaction that stores the list it
// brought (see [Ledger.replaceOfficial]); it needs no import lock. It is best
// effort: any failure, or a page that holds no price, keeps the tool's prices,
// records the attempt time and returns a one-line reason. Without force a
// fresh enough list is left alone.
func refreshOfficial(ctx context.Context, ledger *Ledger, tool Tool, force bool) string {
	label := ratesLabel(tool.Name)
	cached, err := ledger.officialRates(ctx, tool.Name)
	if err != nil {
		return fmt.Sprintf("%s: the official card can't be read (%v)", label, err)
	}
	age := time.Since(fromEpoch(cached.FetchedAt))
	if !force && len(cached.rates()) > 0 && age < ratesMaxAge {
		return cachedText(label, age)
	}
	checked, fetched := epoch(time.Now()), 0.0
	keep := func(msg string) string {
		_ = ledger.recordAttempt(ctx, tool.Name, checked, fetched)
		return msg
	}
	url := tool.Source.PricingURL()
	// The timeout bounds the download only; the ledger is written with ctx.
	download, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	header := http.Header{"User-Agent": {"workbench-costs"}}
	if cached.ETag != "" {
		header.Set("If-None-Match", cached.ETag)
	}
	if cached.LastModified != "" {
		header.Set("If-Modified-Since", cached.LastModified)
	}
	body, response, err := release.Get(download, url, pageLimit, "", header)
	switch {
	case errors.Is(err, release.ErrNotModified) && len(cached.rates()) > 0:
		fetched = epoch(time.Now())
		return keep(label + ": official card not modified")
	case err != nil:
		return keep(fmt.Sprintf("%s: fetch failed (%v); keeping cached card", label, err))
	}
	parsed := tool.Source.ParsePricing(string(body))
	if len(parsed) == 0 {
		return keep(label + ": fetch ok but no model rows parsed; keeping cached card")
	}
	file := officialFile{
		FetchedAt:    epoch(time.Now()),
		CheckedAt:    epoch(time.Now()),
		ETag:         response.Get("ETag"),
		LastModified: response.Get("Last-Modified"),
		URL:          url,
		Rates:        map[string]map[string]float64{},
	}
	for model, rate := range parsed {
		file.Rates[model] = rate.fields()
	}
	if err := ledger.replaceOfficial(ctx, tool.Name, file); err != nil {
		return fmt.Sprintf("%s: fetched but could not save the card (%v)", label, err)
	}
	return fmt.Sprintf("%s: official card updated (%d models)", label, len(parsed))
}

// refreshIfNeeded is the worker's refresh policy: fetch when a tool's official
// card is missing or older than seven days, or when a ledger model has no rate
// anywhere; in all cases at most once a day. home holds the tools' own cost
// records (see calibrated).
func refreshIfNeeded(ctx context.Context, ledger *Ledger, home string) string {
	card, err := loadCard(ctx, home, ledger)
	if err != nil {
		return fmt.Sprintf("rates: the prices can't be read (%v)", err)
	}
	var messages []string
	for _, tool := range Tools {
		if tool.Source == nil || tool.Source.PricingURL() == "" {
			continue
		}
		label := ratesLabel(tool.Name)
		unpriced, err := unpricedModels(ctx, ledger, card, tool)
		if err != nil {
			messages = append(
				messages,
				fmt.Sprintf("%s: the ledger's models can't be read (%v)", label, err),
			)
			continue
		}
		cached, err := ledger.officialRates(ctx, tool.Name)
		if err != nil {
			messages = append(
				messages,
				fmt.Sprintf("%s: the official card can't be read (%v)", label, err),
			)
			continue
		}
		age := time.Since(fromEpoch(cached.FetchedAt))
		sinceCheck := time.Since(fromEpoch(cached.CheckedAt))
		var msg string
		switch {
		case len(cached.rates()) > 0 && age < ratesMaxAge && len(unpriced) == 0:
			msg = cachedText(label, age)
		case sinceCheck >= ratesRetry:
			msg = refreshOfficial(ctx, ledger, tool, true)
		case len(cached.rates()) > 0:
			msg = cachedText(label, age) + ", retry in " + display.Duration(ratesRetry-sinceCheck)
		default:
			msg = label + ": no official card, retry in " + display.Duration(ratesRetry-sinceCheck)
		}
		if len(unpriced) > 0 {
			var names []string
			for _, u := range unpriced {
				names = append(names, fmt.Sprintf("%s (%s)", u.Model, u.Reason))
			}
			msg += "; unpriced: " + strings.Join(names, ", ")
		}
		messages = append(messages, msg)
	}
	return strings.Join(messages, "; ")
}
