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
  minutes: "added to minutes"
skip:
  col: "Active"
  reason: ""
  minutes: "discussion ongoing"
discuss:
  col: "Active"
  reason: ""
  minutes: "discussion ongoing"
comment:
  col: "Active"
  reason: ""
  minutes: "commented"
check:
  col: "Active"
  reason: ""
  minutes: "commented"
  check
hold:
  col: "Hold"
  reason: ""
  minutes: "put on hold"
likely accept:
  col: "Likely Accept"
  reason: ""
  minutes: "**likely accept**; last call for comments ⏳"
accept:
  col: "Accepted"
  reason: ""
  minutes: "no change in consensus; **accepted** 🎉"
likely decline:
  col: "Likely Decline"
  reason: ""
  minutes: "**likely decline**; last call for comments ⏳"
decline:
  col: "Declined"
  reason: ""
  minutes: "no change in consensus; **declined**"
duplicate:
  col: "Declined"
  reason: "duplicate"
  minutes: "duplicate"
retracted:
  col: "Declined"
  reason: "retracted"
  minutes: "proposal retracted by author; **declined**"
infeasible:
  col: "Declined"
  reason: "infeasible"
  minutes: "declined as infeasible"
obsolete:
  col: "Declined"
  reason: "obsolete"
  minutes: "obsolete"
removed:
  col: "none"
  reason: "removed"
  minutes: "removed from proposal process"
hold for #1234:
  col: "Hold"
  reason: ""
  minutes: "hold for #1234"
`
	want = strings.TrimPrefix(want, "\n")

	var got strings.Builder
	check := func(a string) {
		di := Issue{Minutes: a}
		act, err := di.parseActions()
		if err != nil {
			t.Errorf("%s: %s", a, err)
			return
		}
		fmt.Fprintf(&got, "%s:\n  col: %q\n  reason: %q\n  minutes: %q\n", a, act.col, act.reason, act.minutes)
		if act.isCheck {
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
