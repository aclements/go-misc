// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

func FilterInPlace[T any](xs []T, keep func(x T) bool) []T {
	j := 0
	for i := range xs {
		if keep(xs[i]) {
			if i != j {
				xs[i] = xs[j]
			}
			j++
		}
	}
	return xs[:j]
}
