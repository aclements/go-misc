// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []*Benchmark
	}{
		// Test basic line.
		{`
BenchmarkX	1	2 ns/op 3 MB/s`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{}, map[string]float64{"ns/op": 2, "MB/s": 3}},
			},
		},

		// Test short name.
		{`
Benchmark	1	2 ns/op`,
			[]*Benchmark{
				{"", 1, map[string]*Config{}, map[string]float64{"ns/op": 2}},
			},
		},

		// Test bad names.
		{`
Benchmarkx	1	2 ns/op
benchmarkx	1	2 ns/op
benchmarkX	1	2 ns/op`,
			[]*Benchmark{},
		},

		// Test short lines.
		{`
BenchmarkX
BenchmarkX	1
BenchmarkX	1	2`,
			[]*Benchmark{},
		},

		// Test -N.
		{`
BenchmarkX-4	1	2 ns/op`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{
					"gomaxprocs": &Config{"gomaxprocs", "4", "4", false},
				}, map[string]float64{"ns/op": 2}},
			},
		},

		// Test per-benchmark config.
		{`
BenchmarkX/a:20/b:abc	1	2 ns/op
BenchmarkY/c:123	2	4 ns/op`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{
					"a": &Config{"a", "20", "20", false},
					"b": &Config{"b", "abc", "abc", false},
				}, map[string]float64{"ns/op": 2}},
				{"Y", 2, map[string]*Config{
					"c": &Config{"c", "123", "123", false},
				}, map[string]float64{"ns/op": 4}},
			},
		},

		// Test block config.
		{`
commit: 123456
date: Jan 1
colon:colon: 42
blank:
#not-config: x
spa ce: x
funny space: x
Not-config: x
BenchmarkX	1	2 ns/op`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{
					"commit":      &Config{"commit", "123456", "123456", true},
					"date":        &Config{"date", "Jan 1", "Jan 1", true},
					"colon:colon": &Config{"colon:colon", "42", "42", true},
					"blank":       &Config{"blank", "", "", true},
				}, map[string]float64{"ns/op": 2}},
			},
		},

		// Test benchmark config overriding block config.
		{`
commit: 123456
date: Jan 1
BenchmarkX/commit:abcdef	1	2 ns/op`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{
					"commit": &Config{"commit", "abcdef", "abcdef", false},
					"date":   &Config{"date", "Jan 1", "Jan 1", true},
				}, map[string]float64{"ns/op": 2}},
			},
		},

		// Test block config overriding block config.
		{`
commit: 123456
commit: abcdef
date: Jan 1
BenchmarkX	1	2 ns/op`,
			[]*Benchmark{
				{"X", 1, map[string]*Config{
					"commit": &Config{"commit", "abcdef", "abcdef", true},
					"date":   &Config{"date", "Jan 1", "Jan 1", true},
				}, map[string]float64{"ns/op": 2}},
			},
		},
	} {
		r := bytes.NewBufferString(test.input)
		bs, err := Parse(r)
		if err != nil {
			t.Error("unexpected Parse error", err)
			continue
		}
		if !reflect.DeepEqual(bs, test.want) {
			t.Log("want:")
			for _, b := range test.want {
				t.Logf("%#v", b)
			}
			t.Log("got:")
			for _, b := range bs {
				t.Logf("%#v", b)
			}
			t.Fail()
		}
	}
}
