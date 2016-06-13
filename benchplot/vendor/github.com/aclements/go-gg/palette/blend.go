// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package palette

import "image/color"

// blendRGBA returns the interpolation between two sRGB colors with
// pre-multiplied alpha.
func blendRGBA(a, b color.RGBA, x float64) color.RGBA {
	const linThresh = 5
	diff8 := func(a, b uint8) uint8 {
		if a < b {
			return b - a
		}
		return a - b
	}
	if a.A == 255 && b.A == 255 && diff8(a.R, b.R) <= linThresh && diff8(a.G, b.G) <= linThresh && diff8(a.B, b.B) <= linThresh {
		// Perform a quick linear interpolation.
		blend8 := func(a, b uint8, x float64) uint8 {
			c := float64(a)*(1-x) + float64(b)*x
			if c <= 0 {
				return 0
			} else if c >= 255 {
				return 255
			}
			return uint8(c)
		}
		return color.RGBA{
			blend8(a.R, b.R, x),
			blend8(a.G, b.G, x),
			blend8(a.B, b.B, x),
			255,
		}
	}

	blend := func(a, b uint8, x float64, lim uint8) uint8 {
		// Map to linear RGB, blend in linear RGB, and map
		// back to sRGB.
		al, bl := sRGB8ToLinear(a), sRGB8ToLinear(b)
		cl := float64(al)*(1-x) + float64(bl)*x
		if cl < 0 {
			return 0
		} else if cl >= 1<<16-1 {
			return 255
		}
		out := linearTosRGB8(uint16(cl))
		if out > lim {
			out = lim
		}
		return out
	}
	linear := func(a, b uint8, x float64) uint8 {
		c := int(float64(a)*(1-x) + float64(b)*x)
		if c <= 0 {
			return 0
		} else if c >= 255 {
			return 255
		}
		return uint8(c)
	}

	if a.A == b.A {
		// No need to undo the alpha pre-multiplication.
		return color.RGBA{
			blend(a.R, b.R, x, a.A),
			blend(a.G, b.G, x, a.A),
			blend(a.B, b.B, x, a.A),
			a.A,
		}
	}

	// Un-premultiply the alpha, map to linear RGB, blend in
	// linear RGB, map back to sRGB, and re-premultiply the alpha.
	if a.A == 0 {
		return color.RGBA{b.R, b.G, b.B, linear(a.A, b.A, x)}
	} else if b.A == 0 {
		return color.RGBA{a.R, a.G, a.B, linear(a.A, b.A, x)}
	}
	// TODO: This loses precision. Maybe use 16 bit sRGB?
	a.R = uint8(uint16(a.R) * 255 / uint16(a.A))
	a.G = uint8(uint16(a.G) * 255 / uint16(a.A))
	a.B = uint8(uint16(a.B) * 255 / uint16(a.A))
	b.R = uint8(uint16(b.R) * 255 / uint16(b.A))
	b.G = uint8(uint16(b.G) * 255 / uint16(b.A))
	b.B = uint8(uint16(b.B) * 255 / uint16(b.A))
	c := color.RGBA{
		blend(a.R, b.R, x, 255),
		blend(a.G, b.G, x, 255),
		blend(a.B, b.B, x, 255),
		linear(a.A, b.A, x),
	}
	c.R = uint8(uint16(c.R) * uint16(c.A) / 255)
	c.G = uint8(uint16(c.G) * uint16(c.A) / 255)
	c.B = uint8(uint16(c.B) * uint16(c.A) / 255)
	return c
}
