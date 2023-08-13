// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

var sinkInt int

func TestCount0124(t *testing.T) {
	var src strings.Builder
	fmt.Fprintf(&src, "var count0124Tab = [...]uint8{\n")

	for x := 0; x < 256; x++ {
		want := count0124Slow(uint8(x))
		gotFormula := count0124Formula(uint8(x))
		gotTable := count0124(uint8(x))

		t.Logf("%#08b => %d %d %d", x, want, gotFormula, gotTable)
		if want != gotFormula || want != gotTable {
			t.Errorf("count implementations differ for x=%#08b", x)
		}

		fmt.Fprintf(&src, "%d,", want)
		if x%16 == 15 {
			fmt.Fprintf(&src, "\n")
		}
	}

	src.WriteByte('}')
	t.Log(src.String())
}

func BenchmarkCount0124(b *testing.B) {
	// Generate test data.
	var data [1024]byte // Must be a power of 2 for optimal codegen
	rand.Read(data[:])

	b.Run("formula", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124Formula(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
	b.Run("slow", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124Slow(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
	b.Run("table", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
}
