package hazard

import (
	"testing"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
)

func TestEffectOn(t *testing.T) {
	tests := []struct {
		typ  Type
		sev  Severity
		want [3]Effect // wheelchair, stroller, walk_winter
	}{
		{ElevatorOut, Major, [3]Effect{{Block: true}, {Block: true}, {Mult: 1}}},
		{BlockedSidewalk, Major, [3]Effect{{Block: true}, {Block: true}, {Block: true}}},
		{Construction, Minor, [3]Effect{{Block: true}, {Mult: 2}, {Mult: 1.5}}},
		{Construction, Major, [3]Effect{{Block: true}, {Block: true}, {Mult: 3}}},
		{IceSnow, Minor, [3]Effect{{Mult: 3}, {Mult: 2}, {Mult: 4}}},
		{IceSnow, Major, [3]Effect{{Block: true}, {Block: true}, {Mult: 8}}},
		{MissingCurbRamp, Major, [3]Effect{{Block: true}, {Block: true}, {Mult: 1}}},
	}
	for _, tc := range tests {
		for i, p := range cost.Profiles {
			if got := EffectOn(tc.typ, tc.sev, p); got != tc.want[i] {
				t.Errorf("%s/%s/%s: got %+v, want %+v", tc.typ, tc.sev, p, got, tc.want[i])
			}
		}
	}
	// Every effect must keep costs >= length (A* admissibility): block, or multiplier >= 1.
	for _, typ := range Types {
		for _, sev := range []Severity{Minor, Major} {
			for _, p := range cost.Profiles {
				if e := EffectOn(typ, sev, p); !e.Block && e.Mult < 1 {
					t.Errorf("%s/%s/%s: multiplier %.2f < 1", typ, sev, p, e.Mult)
				}
			}
		}
	}
}

func TestNext(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	tests := []struct {
		name    string
		cur     Status
		tally   Tally
		expires time.Time
		want    Status
	}{
		{"new report stays active", Active, Tally{}, later, Active},
		{"one confirm is not enough", Active, Tally{Confirms: 1}, later, Active},
		{"two confirms -> confirmed", Active, Tally{Confirms: 2}, later, Confirmed},
		{"two resolves -> resolved", Confirmed, Tally{Confirms: 3, Resolves: 2}, later, Resolved},
		{"reporter alone can resolve", Active, Tally{ReporterResolved: true}, later, Resolved},
		{"past deadline -> expired", Confirmed, Tally{Confirms: 5}, now, Expired},
		{"resolve wins over expiry", Active, Tally{Resolves: 2}, now.Add(-time.Hour), Resolved},
		{"resolved is final", Resolved, Tally{Confirms: 9}, later, Resolved},
		{"expired is final", Expired, Tally{Confirms: 9}, later, Expired},
	}
	for _, tc := range tests {
		if got := Next(tc.cur, tc.tally, now, tc.expires); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestExtendExpiry(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	// Confirming ice with 1 h left resets it to a full 12 h.
	if got := ExtendExpiry(IceSnow, now.Add(time.Hour), now); !got.Equal(now.Add(12 * time.Hour)) {
		t.Errorf("ice: got %s", got)
	}
	// A deadline already further out than a fresh TTL is never shortened.
	far := now.Add(40 * 24 * time.Hour)
	if got := ExtendExpiry(MissingCurbRamp, far, now); !got.Equal(far) {
		t.Errorf("curb ramp: got %s, want unchanged %s", got, far)
	}
}
