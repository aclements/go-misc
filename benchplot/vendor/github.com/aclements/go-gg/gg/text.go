// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import "unicode/utf8"

type textMetrics struct {
	width   float64
	leading float64
}

// measureString returns the metrics in pixels of s rendered in a font
// with pixel size pxSize.
//
// TODO: Often all I want is the leading, which is much cheaper to get
// than the width. Maybe textMetrics should have methods?
func measureString(pxSize float64, s string) textMetrics {
	// TODO: This is absolutely horribly awful. Make it real,
	// perhaps using the freetype package.

	// Chrome's default font-size is 16px, so 20px is a reasonable
	// leading.
	return textMetrics{
		width:   0.5 * pxSize * float64(utf8.RuneCountInString(s)),
		leading: 1.25 * pxSize,
	}
}
