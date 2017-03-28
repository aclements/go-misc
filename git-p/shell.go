// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "strings"

// shellEscape escapes a single shell token.
func shellEscape(x string) string {
	if len(x) == 0 {
		return "''"
	}
	for _, r := range x {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("@%_-+:,./", r) {
			continue
		}
		// Unsafe character.
		return "'" + strings.Replace(x, "'", "'\"'\"'", -1) + "'"
	}
	return x
}

// shellEscapeList escapes a list of shell tokens.
func shellEscapeList(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = shellEscape(x)
	}
	return strings.Join(out, " ")
}
