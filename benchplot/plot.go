package main

import (
	"fmt"
	"image/color"
	"math"

	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/gg"
	"github.com/aclements/go-gg/ggstat"
	"github.com/aclements/go-gg/table"
)

// TODO: Support plotting non-normalized results.

func plot(t, git table.Grouping, configCols, resultCols []string) (*gg.Plot, int, int) {
	//t = table.Flatten(table.HeadTables(table.GroupBy(t, "name"), 9))

	t = table.Join(t, "commit", git, "commit")

	// Filter to just the master branch.
	//
	// TODO: Flag to control this? Or separate filter command? Or
	// accept a filter expression in the argument?
	t = table.FilterEq(t, "branch", "master")

	// Compute rows and columns.
	ncols := len(resultCols)
	nrows := len(table.GroupBy(t, "name").Tables())

	plot := gg.NewPlot(t)

	// Turn ordered commit date into a "commit index" column.
	plot.SortBy("commit date")
	plot.Stat(commitIndex{})

	// Unpivot all of the metrics into one column.
	plot.Stat(convertFloat{resultCols})
	plot.SetData(table.Unpivot(plot.Data(), "metric", "result", resultCols...))

	// Average each result at each commit (but keep columns names
	// the same to keep things easier to read).
	plot.Stat(ggstat.Agg("commit", "name", "metric", "branch", "commit index")(ggstat.AggMean("result"), ggstat.AggMin("result"), ggstat.AggMax("result")))
	y := "mean result"

	// Normalize to earliest commit on master. It's important to
	// do this before the geomean if there are commits missing.
	// Unfortunately, that also means we have to *temporarily*
	// group by name and metric, since the geomean needs to be
	// done on a different grouping.
	plot.GroupBy("name", "metric")
	plot.Stat(ggstat.Normalize{X: "branch", By: firstMasterIndex, Cols: []string{"mean result", "max result", "min result"}, DenomCols: []string{"mean result", "mean result", "mean result"}})
	y = "normalized " + y
	for _, col := range []string{"mean result", "max result", "min result"} {
		plot.SetData(table.Remove(plot.Data(), col))
	}
	plot.SetData(table.Ungroup(table.Ungroup(plot.Data())))

	// Compute geomean for each metric at each commit if there's
	// more than one benchmark.
	if len(table.GroupBy(t, "name").Tables()) > 1 {
		gt := removeNaNs(plot.Data(), y)
		gt = ggstat.Agg("commit", "metric", "branch", "commit index")(ggstat.AggGeoMean(y), ggstat.AggMin("normalized min result"), ggstat.AggMax("normalized max result")).F(gt)
		gt = table.MapTables(gt, func(_ table.GroupID, t *table.Table) *table.Table {
			return table.NewBuilder(t).AddConst("name", " geomean").Done()
		})
		gt = table.Rename(gt, "geomean "+y, y)
		gt = table.Rename(gt, "min normalized min result", "normalized min result")
		gt = table.Rename(gt, "max normalized max result", "normalized max result")
		plot.SetData(table.Concat(plot.Data(), gt))
		nrows++
	}

	// Always show Y=0.
	plot.SetScale("y", gg.NewLinearScaler().Include(0))

	// Facet by name and metric.
	plot.Add(gg.FacetY{Col: "name"}, gg.FacetX{Col: "metric", SplitYScales: true})

	// Filter the data to reduce noise.
	plot.Stat(kza{y, 15, 3})
	y = "filtered " + y

	plot.Add(gg.LayerArea{
		X:     "commit index",
		Upper: "normalized max result",
		Lower: "normalized min result",
		Fill:  plot.Const(color.Gray{192}),
		//Color: "branch",
	})

	plot.Add(gg.LayerLines{
		X: "commit index",
		Y: y,
		//Color: "branch",
	})
	// plot.Add(gg.LayerTags{X: "commit index", Y: y, Label: "branch"})

	// Interactive tooltip with short hash.
	plot.Stat(tooltip{y})
	plot.Add(gg.LayerTooltips{X: "commit index", Y: y, Label: "tooltip"})

	return plot, nrows, ncols
}

func firstMasterIndex(bs []string) int {
	return slice.Index(bs, "master")
}

type commitIndex struct{}

func (commitIndex) F(g table.Grouping) table.Grouping {
	return table.MapTables(g, func(_ table.GroupID, t *table.Table) *table.Table {
		idxs := make([]int, t.Len())
		last, idx := "", -1
		for i, hash := range t.MustColumn("commit").([]string) {
			if hash != last {
				idx++
				last = hash
			}
			idxs[i] = idx
		}
		t = table.NewBuilder(t).Add("commit index", idxs).Done()

		return t
	})
}

type convertFloat struct {
	cols []string
}

func (c convertFloat) F(g table.Grouping) table.Grouping {
	return table.MapTables(g, func(_ table.GroupID, t *table.Table) *table.Table {
		b := table.NewBuilder(t)
		for _, col := range c.cols {
			var ncol []float64
			slice.Convert(&ncol, t.MustColumn(col))
			b.Add(col, ncol)
		}
		return b.Done()
	})
}

func removeNaNs(g table.Grouping, col string) table.Grouping {
	return table.Filter(g, func(result float64) bool {
		return !math.IsNaN(result)
	}, col)
}

type kza struct {
	X    string
	M, K int
}

func (k kza) F(g table.Grouping) table.Grouping {
	return table.MapTables(g, func(_ table.GroupID, t *table.Table) *table.Table {
		var xs []float64
		slice.Convert(&xs, t.MustColumn(k.X))
		nxs := AdaptiveKolmogorovZurbenko(xs, k.M, k.K)
		return table.NewBuilder(t).Add("filtered "+k.X, nxs).Done()
	})
}

type tooltip struct {
	Y string
}

func (t tooltip) F(g table.Grouping) table.Grouping {
	return table.MapCols(g,
		func(commit []string, result []float64, tooltip []string) {
			for i, c := range commit {
				tooltip[i] = fmt.Sprintf("%s %.2fX", c[:7], result[i])
			}
		}, "commit", t.Y)("tooltip")
}
