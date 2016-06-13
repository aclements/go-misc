// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package palette

//go:generate go run makesrgbtab.go

// sRGB8ToLinear converts 8-bit sRGB component x to a 16-bit linear
// intensity.
func sRGB8ToLinear(x uint8) uint16 {
	return sRGBToLinearTab[x]
}

// linearTosRGB8 converts 16-bit linear intensity x to an 8-bit sRGB
// component.
func linearTosRGB8(x uint16) uint8 {
	return linearTosRGBTab[(uint32(x)+linearTosRGBAddend)>>linearTosRGBShift]
}
