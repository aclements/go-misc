// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"math"

	"github.com/gonum/matrix/mat64"
)

// LinearLeastSquares computes the least squares fit for the function
//
//   f(x) = Β₀terms₀(x) + Β₁terms₁(x) + ...
//
// to the data (xs[i], ys[i]). It returns the parameters Β₀, Β₁, ...
// that minimize the sum of the squares of the residuals of f:
//
//   ∑ (ys[i] - f(xs[i]))²
//
// If weights is non-nil, it is used to weight these residuals:
//
//   ∑ weights[i] × (ys[i] - f(xs[i]))²
//
// The function f is specified by one Go function for each linear
// term. For efficiency, the Go function is vectorized: it will be
// passed a slice of x values in xs and must fill the slice termOut
// with the value of the term for each value in xs.
func LinearLeastSquares(xs, ys, weights []float64, terms ...func(xs, termOut []float64)) (params []float64) {
	// The optimal parameters are found by solving for Β̂ in the
	// "normal equations":
	//
	//    (𝐗ᵀ𝐖𝐗)Β̂ = 𝐗ᵀ𝐖𝐲
	//
	// where 𝐖 is a diagonal weight matrix (or the identity matrix
	// for the unweighted case).

	// TODO: Consider using orthogonal decomposition.

	if len(xs) != len(ys) {
		panic("len(xs) != len(ys)")
	}
	if weights != nil && len(xs) != len(weights) {
		panic("len(xs) != len(weights")
	}

	// Construct 𝐗ᵀ. This is the more convenient representation
	// for efficiently calling the term functions.
	xTVals := make([]float64, len(terms)*len(xs))
	for i, term := range terms {
		term(xs, xTVals[i*len(xs):i*len(xs)+len(xs)])
	}
	XT := mat64.NewDense(len(terms), len(xs), xTVals)
	X := XT.T()

	// Construct 𝐗ᵀ𝐖.
	var XTW *mat64.Dense
	if weights == nil {
		// 𝐖 is the identity matrix.
		XTW = XT
	} else {
		// Since 𝐖 is a diagonal matrix, we do this directly.
		XTW = mat64.DenseCopyOf(XT)
		WDiag := mat64.NewVector(len(weights), weights)
		for row := 0; row < len(terms); row++ {
			rowView := XTW.RowView(row)
			rowView.MulElemVec(rowView, WDiag)
		}
	}

	// Construct 𝐲.
	y := mat64.NewVector(len(ys), ys)

	// Compute Β̂.
	lhs := mat64.NewDense(len(terms), len(terms), nil)
	lhs.Mul(XTW, X)

	rhs := mat64.NewVector(len(terms), nil)
	rhs.MulVec(XTW, y)

	BVals := make([]float64, len(terms))
	B := mat64.NewVector(len(terms), BVals)
	B.SolveVec(lhs, rhs)
	return BVals
}

// PolynomialRegression performs a least squares regression with a
// polynomial of the given degree. It returns the coefficients of the
// best-fit polynomial. If weights is non-nil, it is used to weight
// the residuals.
func PolynomialRegression(degree int, xs, ys, weights []float64) (coefficients []float64) {
	terms := make([]func(xs, termOut []float64), degree+1)
	terms[0] = func(xs, termsOut []float64) {
		for i := range termsOut {
			termsOut[i] = 1
		}
	}
	if degree >= 1 {
		terms[1] = func(xs, termOut []float64) {
			copy(termOut, xs)
		}
	}
	for i := 2; i < len(terms); i++ {
		terms[i] = func(xs, termOut []float64) {
			for i, x := range xs {
				termOut[i] = math.Pow(x, float64(i+1))
			}
		}
	}

	return LinearLeastSquares(xs, ys, weights, terms...)
}
