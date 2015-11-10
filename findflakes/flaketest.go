// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"log"
)

type FlakeTestResult struct {
	All []FlakeRegion
}

type FlakeRegion struct {
	// Times gives the times of all failures in this region, in
	// increasing order.
	//
	// TODO: Remove some of the redundant fields?
	Times []int

	// First and Last are the indexes of the first and last
	// failures in this flaky region. These are equivalent to
	// Times[0] and Times[len(Times)-1], respectively.
	First, Last int

	// Failures is the number of failures in the region. This is
	// equivalent to len(Times).
	Failures int

	// FailureProbability is the fraction of builds in this region
	// that failed.
	FailureProbability float64

	// GoodnessOfFit is the goodness of fit test for this region
	// against the maximum likelihood estimate geometric
	// distribution for these failures. This is primarily for
	// debugging.
	GoodnessOfFit *AndersonDarlingTestResult
}

// FlakeTest finds ranges of commits over which the failure
// probability of a test is fairly consistent. The failures argument
// gives the indexes of commits with failing tests.
//
// This works by assuming flaky tests are a Bernoulli process. That
// is, they fail with some probability and each failure is independent
// of other failures. Using this assumption, it subdivides the failure
// events to find subranges where the distribution of times between
// failures is very similar to a geometric distribution (determined
// using an Anderson-Darling goodness-of-fit test).
func FlakeTest(failures []int) *FlakeTestResult {
	result := &FlakeTestResult{}
	result.subdivide(failures)
	return result
}

// subdivide adds events to the flake test result if it has a strongly
// geometric interarrival distribution. Otherwise, it recursively
// subdivides events on the longest gap.
//
// events must be strictly monotonically increasing.
func (r *FlakeTestResult) subdivide(events []int) {
	if len(events) == 1 {
		// Isolated failure.
		region := FlakeRegion{events, events[0], events[0], 1, 1, nil}
		r.All = append(r.All, region)
		return
	}

	mle, ad := interarrivalAnalysis(events)
	if ad == nil || ad.P >= 0.05 {
		// We failed to reject the null hypothesis that this
		// isn't geometrically distributed. That's about as
		// close as we're going to get to calling it
		// geometrically distributed.
		region := FlakeRegion{events, events[0], events[len(events)-1], len(events), mle.P, ad}
		r.All = append(r.All, region)
		return
	}

	// We reject the null hypothesis and accept the alternate
	// hypothesis that this range of events is not a Bernoulli
	// process. Subdivide on the longest gap, which is the least
	// likely event in this range.
	longestIndex, longestVal := 0, events[1]-events[0]
	for i := 0; i < len(events)-1; i++ {
		val := events[i+1] - events[i]
		if val > longestVal {
			longestIndex, longestVal = i, val
		}
	}

	//fmt.Fprintln(os.Stderr, "subdividing", events[:longestIndex+1], events[longestIndex+1:], mle.P, ad.P)

	// Find the more recent ranges first.
	r.subdivide(events[longestIndex+1:])
	r.subdivide(events[:longestIndex+1])
}

// interarrivalAnalysis returns the maximum likelihood estimated
// distribution for the times between events and the Anderson-Darling
// test for how closely the data matches this distribution. ad will be
// nil if there is no time between any of the events.
//
// events must be strictly monotonically increasing.
func interarrivalAnalysis(events []int) (mle *GeometricDist, ad *AndersonDarlingTestResult) {
	interarrivalTimes := make([]int, len(events)-1)
	sum := 0
	for i := 0; i < len(events)-1; i++ {
		delta := events[i+1] - events[i] - 1
		interarrivalTimes[i] = delta
		sum += delta
	}

	// Compute maximum likelihood estimate of geometric
	// distribution underlying interarrivalTimes.
	mle = &GeometricDist{P: float64(len(interarrivalTimes)) / float64(len(interarrivalTimes)+sum)}
	if mle.P == 1 {
		// This happens if there are no gaps between events.
		// In this case Anderson-Darling is undefined because
		// the CDF is 1.
		return
	}

	// Compute Anderson-Darling goodness-of-fit for the observed
	// distribution against the theoretical distribution.
	var err error
	ad, err = AndersonDarlingTest(interarrivalTimes, mle)
	if err != nil {
		log.Fatal("Anderson-Darling test failed: ", err)
	}

	return
}

func (r *FlakeTestResult) Dump(w io.Writer) {
	for i := range r.All {
		reg := &r.All[len(r.All)-i-1]
		gof := 0.0
		if reg.GoodnessOfFit != nil {
			gof = reg.GoodnessOfFit.P
		}

		fmt.Fprintln(w, reg.First, 0, 0)
		fmt.Fprintln(w, reg.First, reg.FailureProbability, gof)
		fmt.Fprintln(w, reg.Last, reg.FailureProbability, gof)
		fmt.Fprintln(w, reg.Last, 0, 0)
	}
}

// StillHappening returns the probability that the flake is still
// happening as of time t.
func (r *FlakeRegion) StillHappening(t int) float64 {
	if t < r.First {
		return 0
	}
	dist := GeometricDist{P: r.FailureProbability, Start: r.Last + 1}
	return 1 - dist.CDF(t)
}

// Bounds returns the time at which the probability that the failure
// started rises above p and the time at which the probability that
// the failure stopped falls below p. Note that this has no idea of
// the "current" time, so stop may be "in the future."
func (r *FlakeRegion) Bounds(p float64) (start, stop int) {
	dist := GeometricDist{P: r.FailureProbability}
	delta := dist.InvCDF(1 - p)
	return r.First - delta, r.Last + delta
}

// StartedAtOrBefore returns the probability that the failure start at
// or before time t.
func (r *FlakeRegion) StartedAtOrBefore(t int) float64 {
	if t > r.First {
		return 1
	}
	dist := GeometricDist{P: r.FailureProbability}
	return 1 - dist.CDF(r.First-t-1)
}

func (r *FlakeRegion) StartedAt(t int) float64 {
	dist := GeometricDist{P: r.FailureProbability}
	return dist.PMF(r.First - t)
}

// Culprit gives the probability P that the event at time T was
// responsible for a failure.
type Culprit struct {
	P float64
	T int
}

// Culprits returns the possible culprits for this failure up to a
// cumulative probability of cumProb or at most limit events. Culprits
// are returned in reverse time order (from most likely culprit to
// least likely).
func (r *FlakeRegion) Culprits(cumProb float64, limit int) []Culprit {
	culprits := []Culprit{}

	total := 0.0
	for t := r.First; t >= 0 && t > r.First-limit; t-- {
		p := r.StartedAt(t)
		culprits = append(culprits, Culprit{P: p, T: t})
		total += p
		if total > cumProb {
			break
		}
	}

	return culprits
}
