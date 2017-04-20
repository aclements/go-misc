// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

package main

import (
	"math"
	"math/rand"
	"os"

	"github.com/aclements/go-gg/gg"
	"github.com/aclements/go-gg/ggstat"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/vec"
)

func main() {
	xs1 := vec.Linspace(-10, 10, 100)
	for i := range xs1 {
		xs1[i] = rand.Float64()*20 - 10
	}
	ys1 := vec.Map(math.Sin, xs1)

	xs2 := vec.Linspace(-10, 10, 100)
	ys2 := vec.Map(math.Cos, xs2)

	which := []string{}
	for range xs1 {
		which = append(which, "sin")
	}
	for range xs2 {
		which = append(which, "cos")
	}

	xs := vec.Concat(xs1, xs2)
	ys := vec.Concat(ys1, ys2)

	tab := table.NewBuilder(nil).Add("x", xs).Add("y", ys).Add("which", which).Done()

	plot := gg.NewPlot(tab)
	plot.GroupAuto()
	plot.Add(gg.FacetX{Col: "which"})
	plot.Add(gg.FacetY{Col: "which"})
	plot.Add(gg.LayerLines{X: "x", Y: "y"})

	plot.Save()
	plot.SetData(ggstat.ECDF{X: "x"}.F(plot.Data()))
	plot.Add(gg.LayerSteps{Step: gg.StepHV})
	//plot.Add(gg.LayerSteps{Step:gg.StepHMid})
	plot.Restore()

	plot.Save()
	plot.SetData(ggstat.Density{X: "x"}.F(plot.Data()))
	plot.Add(gg.LayerPaths{})
	plot.Restore()

	plot.WriteSVG(os.Stdout, 400, 300)
}
