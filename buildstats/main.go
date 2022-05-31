// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

var since timeFlag

type rev struct {
	path string
	date time.Time

	metaOnce sync.Once
	meta     revMeta
	builders []string
}

type revMeta struct {
	Repo    string   `json:"repo"`
	Results []string `json:"results"`
}

func (r *rev) getMeta() (m revMeta, builders []string) {
	r.metaOnce.Do(func() {
		path := filepath.Join(r.path, ".rev.json")
		b, err := ioutil.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if err = json.Unmarshal(b, &r.meta); err != nil {
			log.Fatalf("decoding %s: %s", path, err)
		}

		path = filepath.Join(r.path, ".builders.json")
		b, err = ioutil.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if err = json.Unmarshal(b, &r.builders); err != nil {
			log.Fatalf("decoding %s: %s", path, err)
		}
	})

	return r.meta, r.builders
}

type stat struct {
	label string

	builds   int
	failures int

	results []result
}

type result int

const (
	resNone result = iota
	resOK
	resFail
)

func (s *stat) failureRate() float64 {
	if s.builds == 0 {
		return 1
	}
	return float64(s.failures) / float64(s.builds)
}

func main() {
	flag.Var(&since, "since", "list only failures on revisions since this date, as an RFC-3339 date or date-time")
	flag.Parse()

	revs := getRevs(since.Time)

	// Collect builders
	stats := make(map[string]*stat)
	var allStats []*stat
	for _, rev := range revs {
		meta, builders := rev.getMeta()
		if meta.Repo != "go" {
			continue
		}
		for _, b := range builders {
			if stats[b] == nil {
				s := &stat{label: b}
				stats[b] = s
				allStats = append(allStats, s)
			}
		}
	}

	for _, rev := range revs {
		m, builders := rev.getMeta()
		bmap := make(map[string]string)
		for i, builder := range builders {
			bmap[builder] = m.Results[i]
		}

		for builder, s := range stats {
			if bmap[builder] == "" {
				s.results = append(s.results, resNone)
				continue
			}
			s.builds++
			if bmap[builder] == "ok" {
				s.results = append(s.results, resOK)
			} else {
				s.failures++
				s.results = append(s.results, resFail)
			}
		}
	}

	sort.Slice(allStats, func(i, j int) bool {
		if allStats[i].failureRate() != allStats[j].failureRate() {
			return allStats[i].failureRate() < allStats[j].failureRate()
		}
		if allStats[i].builds != allStats[j].builds {
			return allStats[i].builds < allStats[j].builds
		}
		return allStats[i].label < allStats[j].label
	})

	fmt.Printf("<!DOCTYPE html>\n")
	fmt.Printf("<html><body>\n")
	fmt.Printf("<table>\n")
	fmt.Printf(`<tr><td>builder</td><td>failures</td><td>%s</td><td align="right">%s</td></tr>`, revs[0].date.Format(rfc3339Date), revs[len(revs)-1].date.Format(rfc3339Date))
	for _, s := range allStats {
		fmt.Printf(`<tr><td>%s</td><td>%6.2f%% (%d/%d)</td><td colspan="2"><img src="%s" /></td></tr>`, html.EscapeString(s.label), 100*s.failureRate(), s.failures, s.builds, pngURI(makeResults(s.results)))
	}
	fmt.Printf("</table>\n")
	fmt.Printf("</body></html>\n")
}

var pathDateRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})-[0-9a-f]+$`)

func getRevs(since time.Time) []*rev {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		log.Fatal("getting cache directory: ", err)
	}
	revDir := filepath.Join(cacheDir, "fetchlogs", "rev")
	dirs, err := os.ReadDir(revDir)
	if err != nil {
		log.Fatalf("reading rev directory %s: %s", revDir, err)
	}

	var matches []*rev
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		name := dir.Name()
		m := pathDateRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		t, err := time.Parse(rfc3339DateTime, m[1])
		if err != nil {
			continue
		}
		if t.Before(since) {
			continue
		}
		matches = append(matches, &rev{
			path: filepath.Join(revDir, name),
			date: t,
		})
	}

	return matches
}

func trueRuns(xs []bool) []int {
	var out []int
	run := 0
	for _, x := range xs {
		if x {
			run++
		} else if run > 0 {
			out = append(out, run)
			run = 0
		}
	}
	if run > 0 {
		out = append(out, run)
	}
	return out
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
