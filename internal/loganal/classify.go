// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loganal

import (
	"regexp"
	"strings"
)

var (
	canonMsg = regexp.MustCompile(`[0-9]+`)

	// numberWords matches words that consist of both letters and
	// digits. Since this is meant to canonicalize numeric fields
	// of error messages, we accept any Unicode letter, but only
	// digits 0-9. We match the whole word to catch things like
	// hexadecimal and temporary file names.
	numberWords = regexp.MustCompile(`\pL*[0-9][\pL0-9]*`)
)

func (f *Failure) canonicalMessage() string {
	// Do we need to do anything to the message?
	for _, c := range f.Message {
		if '0' <= c && c <= '9' {
			goto rewrite
		}
	}
	return f.Message

rewrite:
	// Canonicalize any "word" of the message containing numbers.
	//
	// TODO: "Escape" any existing … to make this safe as a key
	// for later use with canonicalFields (direct use is
	// unimportant).
	return numberWords.ReplaceAllString(f.Message, "…")
}

func (f *Failure) canonicalFields() []string {
	fields := []string{}
	msg := f.Message
	for len(msg) > 0 {
		next := numberWords.FindStringIndex(msg)
		if next == nil {
			fields = append(fields, msg)
			break
		}
		if next[0] > 0 {
			fields = append(fields, msg[:next[0]])
		}
		fields = append(fields, msg[next[0]:next[1]])
		msg = msg[next[1]:]
	}
	return fields
}

// Classify groups a set of failures in to canonicalized failure
// classes. The returned map maps from each failure class to the
// indexes of the input failures in that class. Each input failure
// will be in exactly one failure class.
func Classify(fs []*Failure) map[Failure][]int {
	// Map maximally canonicalized failures to input indexes.
	canon := map[Failure][]int{}
	for i, f := range fs {
		key := Failure{
			Package: f.Package,
			Test:    f.Test,
			Message: f.canonicalMessage(),
			Where:   f.Where, // TODO: Omit line number
		}

		canon[key] = append(canon[key], i)
	}

	// De-canonicalize fields that all of the failures in a class
	// have a common.
	out := make(map[Failure][]int, len(canon))
	for key, class := range canon {
		if len(class) == 1 {
			out[key] = class
			continue
		}

		// Does the message need canonicalization?
		if key.Message != fs[class[0]].Message {
			fields := fs[class[0]].canonicalFields()
			for _, fi := range class[1:] {
				nfields := fs[fi].canonicalFields()
				for i, field := range fields {
					if field != nfields[i] {
						fields[i] = "…"
					}
				}
			}
			key.Message = strings.Join(fields, "")
		}

		// Canonicalize OS and Arch.
		os, arch := fs[class[0]].OS, fs[class[0]].Arch
		for _, fi := range class[1:] {
			if fs[fi].OS != os {
				os = ""
			}
			if fs[fi].Arch != arch {
				arch = ""
			}
		}
		key.OS, key.Arch = os, arch

		out[key] = class
	}

	return out
}
