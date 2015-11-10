// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"math"
	"math/rand"
)

// GeometricDist is a geometric distribution with success probability
// P.
type GeometricDist struct {
	P float64

	// Start is the start of the distribution's support. There are
	// two conventional definitions of the geometric distribution:
	//
	// For Start=0, the distribution gives the number of failures
	// before the first success in a Bernoulli process with
	// success probability P.
	//
	// For Start=1, the distribution gives the number of trials
	// needed to get one success. This is often called the
	// "shifted geometric distribution."
	//
	// Other values of Start are allowed, but have no conventional
	// meaning.
	Start int
}

func (d *GeometricDist) PMF(k int) float64 {
	if k < d.Start {
		return 0
	}
	return math.Pow(1-d.P, float64(k-d.Start)) * d.P
}

func (d *GeometricDist) CDF(k int) float64 {
	if k < d.Start {
		return 0
	}
	return 1 - math.Pow(1-d.P, float64(k-d.Start+1))
}

func (d *GeometricDist) SF(k int) float64 {
	if k < d.Start {
		return 1
	}
	return math.Pow(1-d.P, float64(k-d.Start+1))
}

func (d *GeometricDist) InvCDF(y float64) int {
	return int(math.Ceil(math.Log(1-y)/math.Log(1-d.P)-1)) + d.Start
}

func (d *GeometricDist) Rand() int {
	u := 1 - rand.Float64()
	return int(math.Log(u)/math.Log(1-d.P)) + d.Start
}
