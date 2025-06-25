// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseActions(t *testing.T) {
	want := `
add:
  col: "Active"
  reason: ""
  actions: ["added to minutes"]
skip:
  col: "Active"
  reason: ""
  actions: ["skip"]
discuss:
  col: "Active"
  reason: ""
  actions: ["discussion ongoing"]
comment:
  col: "Active"
  reason: ""
  actions: ["commented"]
check:
  col: "Active"
  reason: ""
  actions: ["commented"]
  check
hold:
  col: "Hold"
  reason: ""
  actions: ["put on hold"]
likely accept:
  col: "Likely Accept"
  reason: ""
  actions: ["**likely accept**; last call for comments ⏳"]
accept:
  col: "Accepted"
  reason: ""
  actions: ["no change in consensus; **accepted** 🎉"]
likely decline:
  col: "Likely Decline"
  reason: ""
  actions: ["**likely decline**; last call for comments ⏳"]
decline:
  col: "Declined"
  reason: ""
  actions: ["no change in consensus; **declined**"]
duplicate:
  col: "Declined"
  reason: "duplicate"
  actions: ["duplicate"]
retracted:
  col: "Declined"
  reason: "retracted"
  actions: ["proposal retracted by author; **declined**"]
infeasible:
  col: "Declined"
  reason: "infeasible"
  actions: ["declined as infeasible"]
obsolete:
  col: "Declined"
  reason: "obsolete"
  actions: ["obsolete"]
removed:
  col: "none"
  reason: "removed"
  actions: ["removed from proposal process"]
hold for #1234:
  col: "Hold"
  reason: ""
  actions: ["hold for #1234"]
`
	want = strings.TrimPrefix(want, "\n")

	var got strings.Builder
	check := func(a string) {
		di := Issue{Minutes: a}
		col, reason, check, actions := di.parseActions()
		fmt.Fprintf(&got, "%s:\n  col: %q\n  reason: %q\n  actions: %q\n", a, col, reason, actions)
		if check {
			fmt.Fprintf(&got, "  check\n")
		}
	}

	// Active states
	check("add")
	check("skip")
	check("discuss")
	check("comment")
	check("check")

	// Hold states
	check("hold")

	// Terminal states
	check("likely accept")
	check("accept")

	check("likely decline")
	check("decline")
	check("duplicate")
	check("retracted")
	check("infeasible")
	check("obsolete")
	check("removed")

	// Hold with comments
	check("hold for #1234")

	if want != got.String() {
		diff(t, want, got.String())
	}
}

func diff(t *testing.T, want, got string) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/want", []byte(want), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/got", []byte(got), 0666); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("diff", "-u", "want", "got")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Errorf("diff failed: %s\nwant:\n%s\ngot:\n%s", err, want, got)
	}
	t.Errorf("\n%s", out)
}
