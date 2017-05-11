// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/aclements/go-gg/table"
)

func ExampleNewTimeScaler() {
	var x []time.Time
	var y []float64
	var steps []time.Duration
	for _, step := range []time.Duration{
		1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
		time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour,
	} {
		t := time.Now()
		for i := 0; i < 100; i++ {
			x = append(x, t)
			y = append(y, rand.Float64()-.5)
			steps = append(steps, 100*step)
			t = t.Add(-step)
		}
	}

	tb := table.NewBuilder(nil)
	tb.Add("x", x).Add("y", y).Add("steps", steps)

	plot := NewPlot(tb.Done())

	plot.SetScale("x", NewTimeScaler())

	plot.Add(FacetY{
		Col:          "steps",
		SplitXScales: true,
	})

	plot.Add(LayerLines{
		X: "x",
		Y: "y",
	})

	f, err := os.Create("scale_time.svg")
	if err != nil {
		panic("unable to create scale_time.svg")
	}
	defer f.Close()
	plot.WriteSVG(f, 800, 1000)
	fmt.Println("ok")
	// output:
	// ok
}
