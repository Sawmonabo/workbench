package costs

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

// EvidenceRank orders the evidence levels: 4 transcript, 3 session, 2
// observed, 1 inferred, 0 unknown or anything else.
func EvidenceRank(evidence string) int {
	switch evidence {
	case EvidenceTranscript:
		return 4
	case EvidenceSession:
		return 3
	case EvidenceObserved:
		return 2
	case EvidenceInferred:
		return 1
	default:
		return 0
	}
}

// evidenceWords are the evidence levels by rank (EvidenceRank).
var evidenceWords = [...]string{
	EvidenceUnknown, EvidenceInferred, EvidenceObserved, EvidenceSession, EvidenceTranscript,
}

// evidenceWord is the evidence level of a stored rank.
func evidenceWord(rank int64) string {
	if rank < 0 || rank >= int64(len(evidenceWords)) {
		return EvidenceUnknown
	}
	return evidenceWords[rank]
}

// storedRank is EvidenceRank of a stored word, ok false for a word that is
// no evidence level: EvidenceRank ranks it as unknown.
func storedRank(word []byte) (rank int, ok bool) {
	rank = EvidenceRank(string(word))
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

// add counts n rows at a level by its rank (EvidenceRank); any other rank
// counts as unknown.
func (e *Evidence) add(rank int, n int64) {
	switch rank {
	case 4:
		e.Transcript += n
	case 3:
		e.Session += n
	case 2:
		e.Observed += n
	case 1:
		e.Inferred += n
	default:
		e.Unknown += n
	}
}
