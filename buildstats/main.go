// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"log"
	"sort"
)

var since timeFlag

type result int8

const (
	resNone result = iota
	resOK
	resFail
)

func resultFromString(s string) result {
	switch s {
	case "":
		return resNone
	case "ok":
		return resOK
	}
	return resFail
}

// grid is a 2D collection of results indexed by a string label and a
// revision.
type grid struct {
	results map[gridKey]result
	labels  map[string]sum
	revs    []*rev
}

type gridKey struct {
	label string
	rev   *rev
}

func newGrid(revs []*rev) *grid {
	return &grid{
		results: make(map[gridKey]result),
		labels:  make(map[string]sum),
		revs:    revs,
	}
}

func (g *grid) add(label string, rev *rev, result result) {
	k := gridKey{label, rev}
	if _, ok := g.results[k]; ok {
		log.Fatalf("duplicate key: (%s, %s)", label, rev)
	}
	g.results[k] = result
	sum := g.labels[label]
	sum.add(result)
	g.labels[label] = sum
	// TODO: Cross-sum on revs, too?
}

// sortedLabels returns the labels of this grid, sorted from highest
// to lowest failure rat.e
func (g *grid) sortedLabels() []string {
	keys := make([]string, 0, len(g.labels))
	for k := range g.labels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return !g.labels[keys[i]].less(g.labels[keys[j]])
	})
	return keys
}

// labelResults returns a slice of results for the given label,
// indexed by rev ID.
func (g *grid) labelResults(label string) []result {
	results := make([]result, len(g.revs))
	for i, rev := range g.revs {
		results[i] = g.results[gridKey{label, rev}]
	}
	return results
}

type sum struct {
	fails int
	total int
}

func (s *sum) add(r result) {
	if r == resNone {
		return
	}
	s.total++
	if r == resFail {
		s.fails++
	}
}

func (s sum) failureRate() float64 {
	if s.total == 0 {
		return 1
	}
	return float64(s.fails) / float64(s.total)
}

func (s sum) less(s2 sum) bool {
	if f1, f2 := s.failureRate(), s2.failureRate(); f1 != f2 {
		return f1 < f2
	}
	return s.total < s2.total
}

func rangeBuildResults(rev *rev, cb func(builder string, res result)) {
	for i, builder := range rev.Builders {
		cb(builder, resultFromString(rev.Results[i]))
	}
}

func main() {
	flag.Var(&since, "since", "list only failures on revisions since this date, as an RFC-3339 date or date-time")
	flag.Parse()

	revs := getRevs(since.Time)
	revs = FilterInPlace(revs, func(r *rev) bool { return r.Repo == "go" })

	g := newGrid(revs)
	for _, rev := range revs {
		rangeBuildResults(rev, func(label string, res result) {
			g.add(label, rev, res)
		})
	}

	fmt.Printf("<!DOCTYPE html>\n")
	fmt.Printf("<html><body>\n")
	fmt.Printf("<table>\n")
	fmt.Printf(`<tr><td>builder</td><td>failures</td><td>%s</td><td align="right">%s</td></tr>`, revs[0].date.Format(rfc3339Date), revs[len(revs)-1].date.Format(rfc3339Date))

	labels := g.sortedLabels()
	for _, label := range labels {
		results := g.labelResults(label)
		sum := g.labels[label]
		fmt.Printf(`<tr><td>%s</td><td>%6.2f%% (%d/%d)</td><td colspan="2"><img src="%s" /></td></tr>`, html.EscapeString(label), 100*sum.failureRate(), sum.fails, sum.total, pngURI(makeResults(results)))
	}

	fmt.Printf("</table>\n")
	fmt.Printf("</body></html>\n")
}

func makeResults(results []result) image.Image {
	// TODO: Hilbert curve?

	var (
		colorNone = color.NRGBA{200, 200, 200, 255}
		colorOK   = color.NRGBA{220, 255, 220, 255}
		colorFail = color.NRGBA{200, 50, 50, 255}
	)

	const px = 3 // Size in pixels of a result
	const h = 6  // Height in results
	w := (len(results) + h - 1) / h
	img := image.NewNRGBA(image.Rect(0, 0, w*px, h*px))
	for i, r := range results {
		c := color.NRGBA{255, 255, 255, 0}
		switch r {
		case resNone:
			c = colorNone
		case resOK:
			c = colorOK
		case resFail:
			c = colorFail
		}

		for dx := 0; dx < px; dx++ {
			for dy := 0; dy < px; dy++ {
				img.SetNRGBA(i/h*px+dx, (i%h)*px+dy, c)
			}
		}
	}
	return img
}

func pngURI(img image.Image) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "data:image/png;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, &buf)
	if err := png.Encode(enc, img); err != nil {
		log.Fatalf("encoding png: %s", err)
	}
	enc.Close()
	return buf.Bytes()
}
