package costs

import (
	"context"
	"time"
)

// Coverage is what the ledger holds for the tool, and when it last ingested.
type Coverage struct {
	First                string   `json:"first"` // YYYY-MM-DD in the machine's time zone, or "none"
	Last                 string   `json:"last"`
	Responses            int64    `json:"responses"`
	AccountEvidence      Evidence `json:"account_evidence"`      // rows by the evidence of their email
	SubscriptionEvidence Evidence `json:"subscription_evidence"` // and of their subscription
	// AccountNamedSince and SubscriptionNamedSince are the time of the first
	// row whose transcript named its email, and its subscription (ledger
	// layout, "" when none did): what the tool's transcripts record starts
	// there.
	AccountNamedSince      string `json:"account_named_since"`
	SubscriptionNamedSince string `json:"subscription_named_since"`
	LastIngestAt           string `json:"last_ingest_at"`
	LastIngestSummary      string `json:"last_ingest_summary"`
	LastError              string `json:"last_error"`
	RatesFetched           string `json:"rates_fetched"` // YYYY-MM-DD in the machine's time zone, or "never"
}

// coverageTally is what a pass over a tool's responses learns for its
// Coverage. Its times are in milliseconds.
type coverageTally struct {
	responses int64
	// first and last are the earliest dated and the latest response time;
	// counted says that a row was counted, and they hold one.
	first, last int64
	counted     bool
	// accountSince and subscriptionSince are the earliest time of a row whose
	// transcript named that, once named is set.
	accountSince, subscriptionSince int64
	accountNamed, subscriptionNamed bool
	account, subscription           Evidence
}

func (t *coverageTally) add(r response) {
	if !t.counted {
		t.first, t.last, t.counted = noTime, noTime, true
	}
	t.responses++
	if r.ts != noTime && (t.first == noTime || r.ts < t.first) {
		t.first = r.ts
	}
	t.last = max(t.last, r.ts)
	if r.accountRank == 4 && (!t.accountNamed || r.ts < t.accountSince) {
		t.accountSince, t.accountNamed = r.ts, true
	}
	if r.subscriptionRank == 4 && (!t.subscriptionNamed || r.ts < t.subscriptionSince) {
		t.subscriptionSince, t.subscriptionNamed = r.ts, true
	}
	t.account.add(r.accountRank, 1)
	t.subscription.add(r.subscriptionRank, 1)
}

// coverage reads every response of tool for its Coverage.
func coverage(
	ctx context.Context,
	ledger *Ledger,
	fetched time.Time,
	tool string,
) (Coverage, error) {
	var tally coverageTally
	if err := scanResponses(ctx, ledger, tool, tally.add); err != nil {
		return Coverage{}, err
	}
	return tally.coverage(ctx, ledger, fetched)
}

// coverage is the tally's Coverage, with the ledger's last ingest and the
// time the official rates were fetched.
func (t coverageTally) coverage(
	ctx context.Context,
	ledger *Ledger,
	fetched time.Time,
) (Coverage, error) {
	first, last := int64(noTime), int64(noTime)
	if t.counted {
		first, last = t.first, t.last
	}
	since := func(ms int64, named bool) string {
		if !named {
			return ""
		}
		return storedTime(ms)
	}
	c := Coverage{
		First: localDate(first), Last: localDate(last), Responses: t.responses,
		AccountEvidence: t.account, SubscriptionEvidence: t.subscription,
		AccountNamedSince:      since(t.accountSince, t.accountNamed),
		SubscriptionNamedSince: since(t.subscriptionSince, t.subscriptionNamed),
	}
	c.LastIngestAt, c.LastIngestSummary, c.LastError = "never", "", ""
	if v, err := ledger.Meta(ctx, "last_ingest_at"); err == nil && v != "" {
		c.LastIngestAt = v
	}
	c.LastIngestSummary, _ = ledger.Meta(ctx, "last_ingest_summary")
	c.LastError, _ = ledger.Meta(ctx, "last_error")
	c.RatesFetched = "never"
	if !fetched.IsZero() {
		c.RatesFetched = fetched.Local().Format(time.DateOnly)
	}
	return c, nil
}
