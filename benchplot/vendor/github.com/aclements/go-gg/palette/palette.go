// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package palette provides palettes and ways to define palettes.
package palette

import (
	"image/color"
	"math"
	"sort"
)

// TODO: Unify continuous and discrete palettes so functions can
// operate on both? Perhaps treat a continuous like a discrete with a
// large number of levels (and a "type" function indicating that it's
// okay to blend between neighboring colors.)

// A Continuous palette is a function from [0, 1] to colors. It may be
// sequential, diverging, or circular.
type Continuous interface {
	Map(x float64) color.Color
}

// RGBGradient is a Continuous palette that interpolates between a
// sequence of colors.
type RGBGradient struct {
	// Colors is the sequence of colors to interpolate between.
	// Interpolation assumes the colors are sRGB values.
	Colors []color.RGBA

	// Stops is an optional sequence of stop positions. It may be
	// nil, in which case Colors are evenly spaced on the interval
	// [0, 1]. Otherwise, it must be a slice with the same length
	// as Colors and must be in ascending order.
	Stops []float64
}

func (g RGBGradient) Map(x float64) color.Color {
	if g.Stops == nil {
		n := x * float64(len(g.Colors)-1)
		ip, fr := math.Modf(n)
		i := int(ip)
		if i <= 0 {
			return g.Colors[0]
		} else if i >= len(g.Colors)-1 {
			return g.Colors[len(g.Colors)-1]
		}
		a, b := g.Colors[i], g.Colors[i+1]
		return blendRGBA(a, b, fr)
	}

	i := sort.SearchFloat64s(g.Stops, x)
	if i == 0 {
		return g.Colors[0]
	} else if i >= len(g.Colors)-1 {
		return g.Colors[len(g.Colors)-1]
	}
	fr := (g.Stops[i] - x) / (g.Stops[i+1] - g.Stops[i])
	a, b := g.Colors[i], g.Colors[i+1]
	return blendRGBA(a, b, fr)
}
