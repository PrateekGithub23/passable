package hazard

import "time"

// Vote thresholds. Two independent people agreeing is the smallest number that filters out a
// single mistaken or malicious tap while still reacting quickly on a small campus.
const (
	ConfirmThreshold = 2
	ResolveThreshold = 2
)

// Tally is the current vote count for one report.
type Tally struct {
	Confirms int
	Resolves int
	// The original reporter said it's gone. Trusted on its own: they know best whether their
	// own report was a mistake or has been cleared.
	ReporterResolved bool
}

// Next computes a report's status after a vote or a clock tick.
//
//	active ──(confirms >= 2)──> confirmed
//	   │                           │
//	   ├──(resolves >= 2 or reporter resolves)──> resolved   (final)
//	   └──(now >= expiresAt)─────────────────────> expired    (final)
//
// Resolution is checked before expiry so a report that was both resolved and past its deadline
// is recorded as resolved (the more informative outcome).
func Next(cur Status, t Tally, now, expiresAt time.Time) Status {
	if !cur.Live() {
		return cur // resolved and expired are final
	}
	if t.ReporterResolved || t.Resolves >= ResolveThreshold {
		return Resolved
	}
	if !now.Before(expiresAt) {
		return Expired
	}
	if t.Confirms >= ConfirmThreshold {
		return Confirmed
	}
	return Active
}

// ExtendExpiry is the new deadline after a confirmation: a full TTL from now, but never
// earlier than the current deadline.
func ExtendExpiry(t Type, cur, now time.Time) time.Time {
	if next := now.Add(TTL(t)); next.After(cur) {
		return next
	}
	return cur
}
