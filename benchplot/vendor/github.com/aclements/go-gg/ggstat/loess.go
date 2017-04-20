// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import (
	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/fit"
	"github.com/aclements/go-moremath/vec"
)

// LOESS constructs a locally-weighted least squares polynomial
// regression for the data (X, Y).
//
// X and Y are required. All other fields have reasonable default zero
// values.
//
// The result of LOESS has two columns in addition to constant columns
// from the input:
//
// - Column X is the points at which the LOESS function is sampled.
//
// - Column Y is the result of the LEOSS function.
//
// TODO: Confidence internals/bootstrap distributions?
//
// TODO: Robust LOESS? See https://www.mathworks.com/help/curvefit/smoothing-data.html#bq_6ys3-3
type LOESS struct {
	// X and Y are the names of the columns to use for X and Y
	// values of data points, respectively.
	X, Y string

	// N is the number of points to sample the regression at. If N
	// is 0, a reasonable default is used.
	N int

	// Domain specifies the domain at which to sample this function.
	// If Domain is nil, it defaults to DomainData{}.
	Domain FunctionDomainer

	// Degree specifies the degree of the local fit function. If
	// it is 0, it is treated as 2.
	Degree int

	// Span controls the smoothness of the fit. If it is 0, it is
	// treated as 0.5. The span must be between 0 and 1, where
	// smaller values fit the data more tightly.
	Span float64
}

func (s LOESS) F(g table.Grouping) table.Grouping {
	if s.Degree <= 0 {
		s.Degree = 2
	}
	if s.Span <= 0 {
		s.Span = 0.5
	}

	var xs, ys []float64
	return Function{
		X: s.X, N: s.N, Domain: s.Domain,
		Fn: func(gid table.GroupID, in *table.Table, sampleAt []float64, out *table.Builder) {
			if len(sampleAt) == 0 {
				out.Add(s.Y, []float64{})
				return
			}

			// TODO: We potentially convert each X column
			// twice, since Function also has to convert
			// them.
			slice.Convert(&xs, in.MustColumn(s.X))
			slice.Convert(&ys, in.MustColumn(s.Y))

			loess := fit.LOESS(xs, ys, s.Degree, s.Span)
			out.Add(s.Y, vec.Map(loess, sampleAt))
		},
	}.F(g)
}
