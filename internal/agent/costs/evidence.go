package costs

import (
	"iter"
	"slices"
	"strconv"
)

// The evidence the account (email) and the subscription of a row each rest on,
// strongest first; the ledger stores them as account_source and
// subscription_source. A stronger copy of a row replaces a weaker one for that
// field, an equal one keeps the stored value.
const (
	// the transcript named it (Codex: the plan, the creator account; Claude
	// Code: the organization)
	EvidenceTranscript = "transcript"
	EvidenceSession    = "session"  // the sign-in a hook bound to the row's root session
	EvidenceObserved   = "observed" // the sign-in observed on both sides of the row's time
	// the account the tool's records put in the row's login stretch, which no
	// record of the row itself names (accountBasis.inferred)
	EvidenceInferred = "inferred"
	EvidenceUnknown  = "unknown" // no evidence (a row keeps an email an earlier build stored, which no report shows)
)

// The ranks of the evidence levels, weakest first, the one place their order
// is stated: the ledger stores a field's evidence as its rank, and a stronger
// level has a higher one.
const (
	rankUnknown = iota
	rankInferred
	rankObserved
	rankSession
	rankTranscript
)

// evidenceLevels are the evidence levels by rank.
var evidenceLevels = [...]string{
	rankUnknown:    EvidenceUnknown,
	rankInferred:   EvidenceInferred,
	rankObserved:   EvidenceObserved,
	rankSession:    EvidenceSession,
	rankTranscript: EvidenceTranscript,
}

// The ranks the ledger's SQL compares stored evidence with, as SQL integer
// literals. A rank is part of a statement's text, never a bound value:
// SQLite reads responses_open only for a query that states the index's
// condition (openRows) in the same words.
var (
	sqlUnknown    = strconv.Itoa(rankUnknown)
	sqlInferred   = strconv.Itoa(rankInferred)
	sqlTranscript = strconv.Itoa(rankTranscript)
)

// evidenceRank is the rank of an evidence level, rankUnknown for anything
// else.
func evidenceRank(evidence string) int {
	return max(slices.Index(evidenceLevels[:], evidence), rankUnknown)
}

// evidenceWord is the evidence level of a stored rank.
func evidenceWord(rank int64) string {
	if rank < 0 || rank >= int64(len(evidenceLevels)) {
		return EvidenceUnknown
	}
	return evidenceLevels[rank]
}

// storedRank is evidenceRank of a stored word, ok false for a word that is
// no evidence level: evidenceRank ranks it as unknown.
func storedRank(word []byte) (rank int, ok bool) {
	rank = evidenceRank(string(word))
	return rank, rank > rankUnknown || string(word) == EvidenceUnknown
}

// Evidence counts rows by the level of evidence one field of them rests on.
type Evidence struct {
	Transcript int64 `json:"transcript"`
	Session    int64 `json:"session"`
	Observed   int64 `json:"observed"`
	Inferred   int64 `json:"inferred"`
	Unknown    int64 `json:"unknown"`
}

// counts are e's counts by rank (evidenceLevels).
func (e *Evidence) counts() [len(evidenceLevels)]*int64 {
	return [...]*int64{
		rankUnknown:    &e.Unknown,
		rankInferred:   &e.Inferred,
		rankObserved:   &e.Observed,
		rankSession:    &e.Session,
		rankTranscript: &e.Transcript,
	}
}

// add counts n rows at a level by its rank (evidenceRank); any other rank
// counts as unknown.
func (e *Evidence) add(rank int, n int64) {
	counts := e.counts()
	if rank < 0 || rank >= len(counts) {
		rank = rankUnknown
	}
	*counts[rank] += n
}

// Levels are e's counts by evidence level, strongest first.
func (e Evidence) Levels() iter.Seq2[string, int64] {
	return func(yield func(level string, rows int64) bool) {
		counts := e.counts()
		for rank := len(counts) - 1; rank >= 0; rank-- {
			if !yield(evidenceLevels[rank], *counts[rank]) {
				return
			}
		}
	}
}
