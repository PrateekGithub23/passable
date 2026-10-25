// Package hazard is the pure domain logic for hazard reports: which types exist, how each one
// affects each routing profile, how long reports live, and the report lifecycle.
//
// Nothing here touches the database or the network, so every rule is a plain function that is
// easy to unit-test. Persistence lives in package reports; routing integration in overlay.
package hazard

import (
	"fmt"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
)

// Type is what kind of problem was reported.
type Type string

const (
	ElevatorOut     Type = "elevator_out"
	BlockedSidewalk Type = "blocked_sidewalk"
	Construction    Type = "construction"
	IceSnow         Type = "ice_snow"
	MissingCurbRamp Type = "missing_curb_ramp"
)

// Types lists every hazard type (the same set the DB CHECK constraint allows).
var Types = []Type{ElevatorOut, BlockedSidewalk, Construction, IceSnow, MissingCurbRamp}

// ParseType validates a hazard type from user input.
func ParseType(s string) (Type, error) {
	for _, t := range Types {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown hazard type %q", s)
}

// Severity separates "slows you down" (minor) from "stops you" (major).
type Severity string

const (
	Minor Severity = "minor"
	Major Severity = "major"
)

// DefaultSeverity is used when the reporter doesn't pick one. Most hazard types are
// all-or-nothing in practice; ice is usually passable with care.
func DefaultSeverity(t Type) Severity {
	if t == IceSnow {
		return Minor
	}
	return Major
}

// ParseSeverity validates a severity; "" means "use the type's default".
func ParseSeverity(s string, t Type) (Severity, error) {
	switch Severity(s) {
	case "":
		return DefaultSeverity(t), nil
	case Minor, Major:
		return Severity(s), nil
	}
	return "", fmt.Errorf("unknown severity %q (want minor or major)", s)
}

// Status is where a report is in its lifecycle (see state.go).
type Status string

const (
	Active    Status = "active"    // reported, not yet confirmed by others
	Confirmed Status = "confirmed" // enough other people confirmed it
	Resolved  Status = "resolved"  // people said it's gone (final)
	Expired   Status = "expired"   // TTL ran out without being re-confirmed (final)
)

// Live reports affect routing; resolved and expired ones are history.
func (s Status) Live() bool { return s == Active || s == Confirmed }

// TTL is how long a report stays live without a new confirmation. It reflects how long each
// kind of problem typically lasts: ice melts or gets salted within hours, construction runs
// for weeks. A confirmation extends the deadline (ExtendExpiry), so reports that people keep
// seeing stay alive and forgotten ones fade out on their own ("decaying" reports).
func TTL(t Type) time.Duration {
	switch t {
	case IceSnow:
		return 12 * time.Hour
	case BlockedSidewalk:
		return 24 * time.Hour
	case ElevatorOut:
		return 48 * time.Hour
	case Construction:
		return 7 * 24 * time.Hour
	case MissingCurbRamp:
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// Effect is what a hazard does to one edge's cost for one profile.
type Effect struct {
	Block bool    // edge becomes impassable
	Mult  float64 // otherwise its cost is multiplied by this (>= 1; 1 = no effect)
}

// EffectOn returns how a hazard of type t and severity sev affects profile p.
//
//	                     wheelchair   stroller   walk_winter
//	elevator_out         block        block      -        (walkers take the stairs)
//	blocked_sidewalk     block        block      block
//	construction         block        x2         x1.5
//	ice_snow             x3           x2         x4       (winter walkers are the most ice-averse)
//	missing_curb_ramp    block        x1.5       -
//
// Major severity turns every soft (multiplier) effect for wheelchair and stroller into a
// block, and doubles walk_winter's multiplier. Multipliers stay >= 1, so A* stays exact.
func EffectOn(t Type, sev Severity, p cost.Profile) Effect {
	// Minor-severity table: [wheelchair, stroller, walk_winter]. Mult 0 means "block".
	const block = 0
	table := map[Type][3]float64{
		ElevatorOut:     {block, block, 1},
		BlockedSidewalk: {block, block, block},
		Construction:    {block, 2, 1.5},
		IceSnow:         {3, 2, 4},
		MissingCurbRamp: {block, 1.5, 1},
	}
	m := table[t][p.Index()]
	if m == block {
		return Effect{Block: true}
	}
	if sev == Major && m > 1 {
		if p == cost.WalkWinter {
			return Effect{Mult: 2 * m}
		}
		return Effect{Block: true}
	}
	return Effect{Mult: m}
}
