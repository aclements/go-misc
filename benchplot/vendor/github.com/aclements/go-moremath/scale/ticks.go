// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

// TickOptions specifies constraints for constructing scale ticks.
//
// A Ticks method will return the ticks at the lowest level (largest
// number of ticks) that satisfies all of the constraints. The exact
// meaning of the tick level differs between scale types, but for all
// scales higher tick levels result in ticks that are further apart
// (fewer ticks in a given interval). In general, the minor ticks are
// the ticks from one level below the major ticks.
type TickOptions struct {
	// Max is the maximum number of major ticks to return.
	Max int

	// MinLevel and MaxLevel are the minimum and maximum tick
	// levels to accept, respectively. If they are both 0, there is
	// no limit on acceptable tick levels.
	MinLevel, MaxLevel int

	// Pred returns true if ticks is an acceptable set of major
	// ticks. ticks will be in increasing order. Pred must be
	// "monotonic" in level in the following sense: if Pred is
	// false for level l (or ticks t), it must be false for all l'
	// < l (or len(t') > len(t)), and if Pred is true for level l
	// (or ticks t), it must be true for all l' > l (or len(t') <
	// len(t)). In other words, Pred should return false if there
	// are "too many" ticks or they are "too close together".
	//
	// If Pred is nil, it is assumed to always be satisfied.
	Pred func(ticks []float64, level int) bool
}

// FindLevel returns the lowest level that satisfies the constraints
// given by o:
//
// * count(level) <= o.Max
//
// * o.MinLevel <= level <= o.MaxLevel (if MinLevel and MaxLevel != 0).
//
// * o.Pred(ticks(level), level) is true (if o.Pred != nil).
//
// If the constraints cannot be satisfied, it returns 0, false.
//
// ticks(level) must return the tick marks at level in increasing
// order. count(level) must return len(ticks(level)), but should do so
// without constructing the ticks array because it may be very large.
// count must be a weakly monotonically decreasing function of level.
// guess is the level to start the optimization at.
func (o *TickOptions) FindLevel(count func(level int) int, ticks func(level int) []float64, guess int) (int, bool) {
	minLevel, maxLevel := o.MinLevel, o.MaxLevel
	if minLevel == 0 && maxLevel == 0 {
		minLevel, maxLevel = -1000, 1000
	} else if minLevel > maxLevel {
		return 0, false
	}
	if o.Max < 1 {
		return 0, false
	}

	// Start with the initial guess.
	l := guess
	if l < minLevel {
		l = minLevel
	} else if l > maxLevel {
		l = maxLevel
	}

	// Optimize count against o.Max.
	if count(l) <= o.Max {
		// We're satisfying the o.Max and min/maxLevel
		// constraints. count is monotonically decreasing, so
		// decrease level to increase the count until we
		// violate either o.Max or minLevel.
		for l--; l >= minLevel && count(l) <= o.Max; l-- {
		}
		// We went one too far.
		l++
	} else {
		// We're over o.Max. Increase level to decrease the
		// count until we go below o.Max. This may cause us to
		// violate maxLevel.
		for l++; l <= maxLevel && count(l) > o.Max; l++ {
		}
		if l > maxLevel {
			// We can't satisfy both o.Max and maxLevel.
			return 0, false
		}
	}

	// At this point l is the lowest value that satisfies the
	// o.Max, minLevel, and maxLevel constraints.

	// Optimize ticks against o.Pred.
	if o.Pred != nil {
		// Increase level until Pred is satisfied. This may
		// cause us to violate maxLevel.
		for l <= maxLevel && !o.Pred(ticks(l), l) {
			l++
		}
		if l > maxLevel {
			// We can't satisfy both maxLevel and Pred.
			return 0, false
		}
	}

	return l, true
}
