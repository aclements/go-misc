// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dashquery

import (
	"context"
	"encoding/json"
	"fmt"
	"go/constant"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/build/types"
	"golang.org/x/sync/errgroup"
)

func Compile(expr string) (*Query, error) {
	c := newCompiler(builtins)
	fn, err := c.compile(expr)
	if err != nil {
		return nil, err
	}
	return &Query{fn}, nil
}

func constNum(v int64) numberNode {
	cv := constant.MakeInt64(v)
	return numberNode(func(pi pathInfo) constant.Value {
		return cv
	})
}

var startTimeCache time.Time

func startTime() time.Time {
	if startTimeCache.IsZero() {
		startTimeCache = time.Now()
	}
	return startTimeCache
}

var builtins = map[string]queryNode{
	"true": boolNode(func(pi pathInfo) bool {
		return true
	}),
	"false": boolNode(func(pi pathInfo) bool {
		return false
	}),

	"second":  constNum(int64(time.Second)),
	"seconds": constNum(int64(time.Second)),
	"minute":  constNum(int64(time.Minute)),
	"minutes": constNum(int64(time.Minute)),
	"hour":    constNum(int64(time.Hour)),
	"hours":   constNum(int64(time.Hour)),
	"day":     constNum(int64(24 * time.Hour)),
	"days":    constNum(int64(24 * time.Hour)),

	"age": numberNode(func(pi pathInfo) constant.Value {
		date, err := time.Parse(time.RFC3339, pi.buildRev().Date)
		if err != nil {
			return constant.MakeInt64(0)
		}
		return constant.MakeInt64(int64(startTime().Sub(date)))
	}),

	"builder": stringNode(func(pi pathInfo) string {
		return pi.builder
	}),
	"os": stringNode(func(pi pathInfo) string {
		i := strings.IndexByte(pi.builder, '-')
		if i < 0 {
			return "?"
		}
		return pi.builder[:i]
	}),
	"arch": stringNode(func(pi pathInfo) string {
		i := strings.IndexByte(pi.builder, '-') + 1
		if i <= 0 {
			return "?"
		}
		i2 := strings.IndexByte(pi.builder[i:], '-')
		if i2 < 0 {
			return pi.builder[i:]
		}
		return pi.builder[i : i+i2]
	}),

	// TODO: From .rev.json: repo, revision, date, branch, author
	// TODO: File content matching
}

type Query struct {
	fn boolNode
}

type pathInfo struct {
	builder       string
	revPath       string
	buildRevCache *types.BuildRevision
}

func (pi *pathInfo) buildRev() *types.BuildRevision {
	if pi.buildRevCache == nil {
		pi.buildRevCache = new(types.BuildRevision)
		data, err := ioutil.ReadFile(filepath.Join(pi.revPath, ".rev.json"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else {
			err = json.Unmarshal(data, pi.buildRevCache)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
	return pi.buildRevCache
}

func RevDir() string {
	return filepath.Join(xdgCacheDir(), "fetchlogs", "rev")
}

// revs returns revision paths in reverse chronological order.
func revs() ([]string, error) {
	revDir := RevDir()
	fis, err := ioutil.ReadDir(revDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, fi := range fis {
		if !fi.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(revDir, fi.Name()))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}

// AllPaths finds all dashboard log paths matching q and passes them
// to fn. Paths are returned in descending order by date (most recent
// first).
func (q *Query) AllPaths(fn func(string) error) error {
	revs, err := revs()
	if err != nil {
		return err
	}

	type task struct {
		pi    pathInfo
		reply chan bool
	}
	nworkers := 2 * runtime.GOMAXPROCS(-1)
	tasks := make(chan task)
	replies := make(chan task, nworkers)
	g, ctx := errgroup.WithContext(context.Background())

	// Feeder.
	g.Go(func() error {
		defer close(tasks)
		defer close(replies)

		var pi pathInfo
		for _, rev := range revs {
			pi.revPath = rev

			logs, err := ioutil.ReadDir(rev)
			if err != nil {
				return err
			}

			for _, log := range logs {
				if log.IsDir() || strings.HasPrefix(log.Name(), ".") {
					continue
				}
				pi.builder = log.Name()

				task := task{pi, make(chan bool)}
				select {
				case tasks <- task:
					replies <- task
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		return nil
	})

	// Workers.
	for i := 0; i < nworkers; i++ {
		g.Go(func() error {
			for {
				task, ok := <-tasks
				if !ok {
					break
				}
				task.reply <- q.fn(task.pi)
			}
			return nil
		})
	}

	// Aggregator.
	g.Go(func() error {
		for reply := range replies {
			if <-reply.reply {
				pi := reply.pi
				err := fn(filepath.Join(pi.revPath, pi.builder))
				if err != nil {
					return err
				}
			}
		}
		return nil
	})

	return g.Wait()
}
