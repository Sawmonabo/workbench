package costs

import (
	"iter"
	"slices"
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

// evidenceLevels are the evidence levels, weakest first: a level's index is
// its rank (evidenceRank), which the ledger stores.
var evidenceLevels = [...]string{
	EvidenceUnknown, EvidenceInferred, EvidenceObserved, EvidenceSession, EvidenceTranscript,
}

// evidenceRank orders the evidence levels: 4 transcript, 3 session, 2
// observed, 1 inferred, 0 unknown or anything else.
func evidenceRank(evidence string) int {
	return max(slices.Index(evidenceLevels[:], evidence), 0)
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
	return rank, rank > 0 || string(word) == EvidenceUnknown
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
	return [...]*int64{&e.Unknown, &e.Inferred, &e.Observed, &e.Session, &e.Transcript}
}

// add counts n rows at a level by its rank (evidenceRank); any other rank
// counts as unknown.
func (e *Evidence) add(rank int, n int64) {
	counts := e.counts()
	if rank < 0 || rank >= len(counts) {
		rank = 0
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
