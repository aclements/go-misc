// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import (
	"math"

	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/stats"
	"github.com/aclements/go-moremath/vec"
)

// TODO: Default to first (and second) column for X (and Y)?

// Density constructs a probability density estimate from a set of
// samples using kernel density estimation.
//
// X is the only required field. All other fields have reasonable
// default zero values.
//
// The result of Density has three columns in addition to constant
// columns from the input:
//
// - Column X is the points at which the density estimate is sampled.
//
// - Column "probability density" is the density estimate.
//
// - Column "cumulative density" is the cumulative density estimate.
type Density struct {
	// X is the name of the column to use for samples.
	X string

	// W is the optional name of the column to use for sample
	// weights. It may be "" to uniformly weight samples.
	W string

	// N is the number of points to sample the KDE at. If N is 0,
	// a reasonable default is used.
	//
	// TODO: This is particularly sensitive to the scale
	// transform.
	N int

	// Widen controls the domain of the returned density estimate.
	// If Widen is < 0, the domain is the range of the data.
	// Otherwise, the domain will be expanded by Widen*Bandwidth
	// (which may be the computed bandwidth). If Widen is 0, it is
	// replaced with a default value of 3.
	//
	// TODO: This is different from ECDF and LeastSquares. Make
	// them consistent.
	Widen float64

	// SplitGroups indicates that each group in the table should
	// have separate bounds based on the data in that group alone.
	// The default, false, indicates that the density function
	// should use the bounds of all of the data combined. This
	// makes it possible to stack KDEs and easier to compare KDEs
	// across groups.
	SplitGroups bool

	// Kernel is the kernel to use for the KDE.
	Kernel stats.KDEKernel

	// Bandwidth is the bandwidth to use for the KDE.
	//
	// If this is zero, the bandwidth is computed from the data
	// using a default bandwidth estimator (currently
	// stats.BandwidthScott).
	Bandwidth float64

	// BoundaryMethod is the boundary correction method to use for
	// the KDE. The default value is BoundaryReflect; however, the
	// default bounds are effectively +/-inf, which is equivalent
	// to performing no boundary correction.
	BoundaryMethod stats.KDEBoundaryMethod

	// [BoundaryMin, BoundaryMax) specify a bounded support for
	// the KDE. If both are 0 (their default values), they are
	// treated as +/-inf.
	//
	// To specify a half-bounded support, set Min to math.Inf(-1)
	// or Max to math.Inf(1).
	BoundaryMin float64
	BoundaryMax float64
}

func (d Density) F(g table.Grouping) table.Grouping {
	kde := stats.KDE{
		Kernel:         d.Kernel,
		Bandwidth:      d.Bandwidth,
		BoundaryMethod: d.BoundaryMethod,
		BoundaryMin:    d.BoundaryMin,
		BoundaryMax:    d.BoundaryMax,
	}
	if d.N == 0 {
		d.N = 200
	}
	if d.Widen == 0 {
		d.Widen = 3
	}
	dname, cname := "probability density", "cumulative density"

	// Gather samples.
	samples := map[table.GroupID]stats.Sample{}
	for _, gid := range g.Tables() {
		t := g.Table(gid)
		var sample stats.Sample
		slice.Convert(&sample.Xs, t.MustColumn(d.X))
		if d.W != "" {
			slice.Convert(&sample.Weights, t.MustColumn(d.W))
		}
		samples[gid] = sample
	}

	min, max := math.NaN(), math.NaN()
	if !d.SplitGroups {
		// Compute combined bounds.
		for _, sample := range samples {
			smin, smax := sample.Bounds()
			if math.IsNaN(smin) {
				continue
			}

			bandwidth := d.Bandwidth
			if d.Bandwidth == 0 {
				bandwidth = stats.BandwidthScott(sample)
			}

			smin, smax = smin-d.Widen*bandwidth, smax+d.Widen*bandwidth
			if smin < min || math.IsNaN(min) {
				min = smin
			}
			if smax > max || math.IsNaN(max) {
				max = smax
			}
		}
	}

	return table.MapTables(g, func(gid table.GroupID, t *table.Table) *table.Table {
		kde.Sample = samples[gid]

		if kde.Sample.Weight() == 0 {
			return new(table.Builder).Add(d.X, []float64{}).Add(dname, []float64{}).Add(cname, []float64{}).Done()
		}

		if d.Bandwidth == 0 {
			kde.Bandwidth = stats.BandwidthScott(kde.Sample)
		}

		if d.SplitGroups {
			// Compute group bounds.
			min, max = kde.Sample.Bounds()
			min, max = min-d.Widen*kde.Bandwidth, max+d.Widen*kde.Bandwidth
		}

		ss := vec.Linspace(min, max, d.N)
		nt := new(table.Builder).Add(d.X, ss)
		nt.Add(dname, vec.Map(kde.PDF, ss))
		nt.Add(cname, vec.Map(kde.CDF, ss))
		preserveConsts(nt, t)
		return nt.Done()
	})
}
