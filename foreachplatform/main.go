// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
)

type Platform struct {
	GOOS   string
	GOARCH string
	SetCgo bool
	Cgo    bool
	Race   bool
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: foreachplatform [-list | command]\n\n")
		fmt.Fprintf(os.Stderr, "Run command with every Go platform environment.\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n\n")
		fmt.Fprintf(os.Stderr, "Check that the runtime builds in all configurations:\n")
		fmt.Fprintf(os.Stderr, "\tforeachplatform go test -c runtime\n")
	}
	flagList := flag.Bool("list", false, "list platforms instead of running a command")
	flag.Parse()
	subcmd := flag.Args()
	if *flagList && len(subcmd) > 0 {
		fmt.Fprintf(os.Stderr, "cannot use both -list and command\n")
		os.Exit(2)
	}
	if !*flagList && len(subcmd) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	plats := getPlatforms()
	if *flagList {
		for _, plat := range plats {
			fmt.Println(plat)
		}
		return
	}

	// TODO: Check if there are any source files *not* covered by plats.

	// TODO: Run platforms in parallel.

	failed := false
	for _, plat := range plats {
		fmt.Fprintf(os.Stderr, "# %s\n", plat.String())
		var buf strings.Builder
		cmd := exec.Command(subcmd[0], subcmd[1:]...)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmd.Env = append(cmd.Environ(), plat.Env()...)
		err := cmd.Run()
		if err != nil {
			if plat.FailOK(buf.String()) {
				fmt.Fprintf(os.Stderr, "# (ignoring expected failure)\n")
				continue
			}
			fmt.Fprintf(os.Stderr, "%s", buf.String())
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func (p Platform) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "GOOS=%-9s GOARCH=%s", p.GOOS, p.GOARCH)
	if p.SetCgo {
		fmt.Fprintf(&b, " CGO_ENABLED=%-5v", p.Cgo)
	}
	if p.Race {
		fmt.Fprintf(&b, " GOFLAGS=-race")
	}
	return b.String()
}

func (p Platform) Env() []string {
	env := []string{"GOOS=" + p.GOOS, "GOARCH=" + p.GOARCH}
	if p.SetCgo {
		if p.Cgo {
			env = append(env, "CGO_ENABLED=1")
		} else {
			env = append(env, "CGO_ENABLED=0")
		}
	}
	if p.Race {
		env = append(env, "GOFLAGS=-race")
	}
	return env
}

func (p Platform) FailOK(msg string) bool {
	if p.GOOS == "android" || p.GOOS == "ios" {
		if strings.Contains(msg, "loadinternal: cannot find runtime/cgo\n") {
			return true
		}
	}
	return false
}

func goTool[T any](subcmd ...string) T {
	cmd := exec.Command("go", subcmd...)
	data, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}

	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		log.Fatalf("go %s: error parsing output: %s", strings.Join(subcmd, " "), err)
	}
	return out
}

func getPlatforms() []Platform {
	var plats []Platform

	env := goTool[map[string]string]("env", "-json")

	// Add the host GOOS/GOARCH, with different combinations of Cgo and Race.
	host := Platform{
		GOOS:   env["GOOS"],
		GOARCH: env["GOARCH"],
	}
	cgos := []bool{false}
	var setCgo bool
	switch env["CGO_ENABLED"] {
	case "0":
		setCgo = true
	case "1":
		cgos = []bool{true, false}
		setCgo = true
	}
	for _, race := range []bool{false, true} {
		host.Race = race
		for _, cgo := range cgos {
			host.Cgo = cgo
			host.SetCgo = setCgo
			if race && !cgo {
				// cgo requires race.
				continue
			}
			plats = append(plats, host)
		}
	}

	// Add the other platforms.
	type distPlatform struct {
		GOOS       string
		GOARCH     string
		FirstClass bool
	}
	distList := goTool[[]distPlatform]("tool", "dist", "list", "-json")
	slices.SortFunc(distList, func(a, b distPlatform) int {
		// Sort first-class ports first, then our host OS, then alphabetically
		// by GOOS, then GOARCH.
		return or(
			trueFalse(a.FirstClass, b.FirstClass),
			trueFalse(a.GOOS == host.GOOS, b.GOOS == host.GOOS),
			cmp.Compare(a.GOOS, b.GOOS),
			cmp.Compare(a.GOARCH, b.GOARCH))
	})
	for _, distPlatform := range distList {
		if distPlatform.GOOS == host.GOOS && distPlatform.GOARCH == host.GOARCH {
			continue
		}
		// In general we can't build cgo on any non-host platform, so we
		// ignore that dimension.
		//
		// TODO: In some cases we can.
		plats = append(plats, Platform{
			GOOS:   distPlatform.GOOS,
			GOARCH: distPlatform.GOARCH,
		})
	}

	return plats
}

func trueFalse(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return -1
	}
	return 1
}

func or[T comparable](vals ...T) T {
	var zero T
	for _, val := range vals {
		if val != zero {
			return val
		}
	}
	return zero
}
