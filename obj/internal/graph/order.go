// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import "math/big"

// PreOrder returns the nodes of g visited in pre-order.
func PreOrder(g Graph, root int) []int {
	const stackNodes = 1024
	var words [stackNodes / 32]big.Word
	var visited big.Int
	visited.SetBits(words[:]) // Keep small graphs on the stack

	out := []int{}
	var visit func(n int)
	visit = func(n int) {
		out = append(out, n)
		visited.SetBit(&visited, n, 1)
		for _, succ := range g.Out(n) {
			if visited.Bit(succ) == 0 {
				visit(succ)
			}
		}
	}
	visit(root)

	return out
}

// PostOrder returns the nodes of g visited in post-order.
func PostOrder(g Graph, root int) []int {
	const stackNodes = 1024
	var words [stackNodes / 32]big.Word
	var visited big.Int
	visited.SetBits(words[:]) // Keep small graphs on the stack

	out := []int{}
	var visit func(n int)
	visit = func(n int) {
		visited.SetBit(&visited, n, 1)
		for _, succ := range g.Out(n) {
			if visited.Bit(succ) == 0 {
				visit(succ)
			}
		}
		out = append(out, n)
	}
	visit(root)

	return out
}

// Reverse reverses xs in place and returns the slice. This is useful
// in conjunction with PreOrder and PostOrder.
func Reverse(xs []int) []int {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
	return xs
}
