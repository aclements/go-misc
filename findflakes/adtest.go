// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrSampleSize = errors.New("sample is too small")
)

type SampleValueError struct {
	Value  int
	Detail string
}

func (e *SampleValueError) Error() string {
	return e.Detail
}

type AndersonDarlingTestResult struct {
	// A2 is the Anderson-Darling test statistic, A², for the
	// goodness of fit of the sample to the probability
	// distribution.
	A2 float64

	// P is the p-value for this test. A small value of P
	// indicates a significant difference between the sample and
	// the distribution.
	P float64
}

// AndersonDarlingTest performs an Anderson-Darling goodness-of-fit
// test for whether a sample comes from a population with a specified
// distribution. It tests the null hypothesis that sample follows dist
// against the alternate hypothesis that sample does not follow dist.
//
// Note that this uses a Monte Carlo method (parametric bootstrap) to
// estimate the distribution of the test statistic and hence the exact
// P value may vary slightly between calls with the same sample and
// distribution.
func AndersonDarlingTest(sample []int, dist *GeometricDist) (*AndersonDarlingTestResult, error) {
	if len(sample) == 0 {
		return nil, ErrSampleSize
	}

	if !sort.IntsAreSorted(sample) {
		sample = append([]int(nil), sample...)
		sort.Ints(sample)
	}

	A2, err := andersonDarling(sample, dist)
	if err != nil {
		return nil, err
	}

	// Use parametric bootstrap to estimate the distribution of
	// A².
	const resamples = 1000
	nsample := make([]int, len(sample))
	ngreater := 0
	for i := 0; i < resamples; i++ {
		for j := range nsample {
			nsample[j] = dist.Rand()
		}
		sort.Ints(nsample)
		nA2, err := andersonDarling(nsample, dist)
		if err != nil {
			return nil, err
		}
		if nA2 >= A2 {
			ngreater++
		}
	}
	p := float64(ngreater) / resamples

	return &AndersonDarlingTestResult{A2, p}, nil
}

// andersonDarling returns the Anderson-Darling test statistic, A²,
// for the goodness of fit of sample to dist.
//
// sample must be sorted.
func andersonDarling(sample []int, dist *GeometricDist) (float64, error) {
	sum := 0.0
	// TODO: Rearrange terms so we don't have to compute each
	// sample's CDF twice.
	for i, y1 := range sample {
		y2 := sample[len(sample)-i-1]
		cdf1, sf2 := dist.CDF(y1), dist.SF(y2)
		if cdf1 == 0 {
			return 0, &SampleValueError{
				Value:  y1,
				Detail: fmt.Sprintf("sample %d lies outside support of expected distribution %v", y1, dist),
			}
		}
		if sf2 == 0 {
			return 0, &SampleValueError{
				Value:  y2,
				Detail: fmt.Sprintf("sample %d lies outside support of expected distribution %v", y2, dist),
			}
		}
		sum += float64(2*i-1) * (math.Log(cdf1) + math.Log(sf2))
	}
	return -float64(len(sample)) - sum/float64(len(sample)), nil
}
