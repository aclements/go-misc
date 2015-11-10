// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loganal

import (
	"regexp"
	"strings"
)

// Failure records a failure extracted from an all.bash log.
type Failure struct {
	// Package is the Go package of this failure. In the case of a
	// testing.T failure, this will be the package of the test.
	Package string

	// Test identifies the failed test function. If this is not a
	// testing.T failure, this will be "".
	Test string

	// Message is the failure message.
	Message string

	// Where indicates where this failure happened. If this is a
	// regular test failure, this will be the file and line of the
	// last testing.T.Errorf call. If this is a panic, this will
	// be the fully qualified name of the function where this
	// failure happened (this helps distinguish between generic
	// errors like "out of bounds").
	Where string

	// OS and Arch are the GOOS and GOARCH of this failure.
	OS, Arch string
}

func (f Failure) String() string {
	s := f.Package
	if f.Test != "" {
		s += "." + f.Test
	}
	if f.Where != "" {
		if s != "" {
			s += " "
		}
		s += "at " + f.Where
	}
	if s != "" {
		s += ": "
	}
	s += f.Message
	return s
}

var (
	linesStar = `(?:.*\n)*?`
	linesPlus = `(?:.*\n)+?`
	failPkg   = `(?m:^FAIL[ \t]+(\S+))`

	canonLine = regexp.MustCompile(`\r+\n`)

	// testingHeader matches the beginning of the go test std
	// section. On Plan 9 there used to be just one #.
	testingHeader = regexp.MustCompile(`^#+ Testing packages`)

	// sectionHeader matches the header of each testing section
	// printed by go tool dist test.
	sectionHeader = regexp.MustCompile(`^##### (.*)`)

	// testingFailed matches a testing.T failure. This may be a
	// T.Error or a recovered panic.
	testingFailed = regexp.MustCompile(`^--- FAIL: (\S+).*\n(` + linesStar + `)` + failPkg)

	// testingError matches the file name and message of the last
	// T.Error in a testingFailed log.
	testingError = regexp.MustCompile(`(?:.*\n)*\t([^:]+:[0-9]+): (.*)\n`)

	// testingPanic matches a recovered panic in a testingFailed
	// log.
	testingPanic = regexp.MustCompile(`panic: (.*?)(?: \[recovered\])`)

	// gotestFailed matches a $GOROOT/test failure.
	gotestFailed = regexp.MustCompile(`^# go run run\.go.*\n(` + linesPlus + `)` + failPkg)

	// buildFailed matches build failures from the testing package.
	buildFailed = regexp.MustCompile(`^` + failPkg + `\s+\[build failed\]`)

	// timeoutPanic1 matches a test timeout detected by the testing package.
	timeoutPanic1 = regexp.MustCompile(`^panic: test timed out after .*\n(` + linesStar + `)` + failPkg)

	// timeoutPanic2 matches a test timeout detected by go test.
	timeoutPanic2 = regexp.MustCompile(`^\*\*\* Test killed.*ran too long\n` + failPkg)

	// coordinatorTimeout matches a test timeout detected by the
	// coordinator, for both non-sharded and sharded tests.
	coordinatorTimeout = regexp.MustCompile(`(?m)^Build complete.*Result: error: timed out|^Test "[^"]+" ran over [0-9a-z]+ limit`)

	// tbEntry is a regexp string that matches a single
	// function/line number entry in a traceback. Group 1 matches
	// the fully qualified function name.
	tbEntry = `(\S+)\(.*\)\n\t.*:[0-9]+ .*\n`

	// runtimeFailed matches a runtime throw or testing package
	// panic. Matching the panic is fairly loose because in some
	// cases a "fatal error:" can be preceded by a "panic:" if
	// we've started the panic and then realize we can't (e.g.,
	// sigpanic). Also gather up the "runtime:" prints preceding a
	// throw.
	runtimeFailed        = regexp.MustCompile(`^(?:runtime:.*\n)*.*(?:panic: |fatal error: )(.*)`)
	runtimeLiterals      = []string{"runtime:", "panic:", "fatal error:"}
	runtimeFailedTrailer = regexp.MustCompile(`^(?:exit status.*\n)?(?:\*\*\* Test killed.*\n)?(?:` + failPkg + `)?`)

	// apiCheckerFailed matches an API checker failure.
	apiCheckerFailed = regexp.MustCompile(`^Error running API checker: (.*)`)

	// goodLine matches known-good lines so we can ignore them
	// before doing more aggressive/fuzzy failure extraction.
	goodLine = regexp.MustCompile(`^#|^ok\s|^\?\s|^Benchmark|^PASS|^=== |^--- `)

	// testingUnknownFailed matches the last line of some unknown
	// failure detected by the testing package.
	testingUnknownFailed = regexp.MustCompile(`^` + failPkg)

	// miscFailed matches the log.Fatalf in go tool dist test when
	// a test fails. We use this as a last resort, mostly to pick
	// up failures in sections that don't use the testing package.
	miscFailed = regexp.MustCompile(`^.*Failed: (?:exit status|test failed)`)
)

// Extract parses the failures from all.bash log m.
func Extract(m string, os, arch string) ([]*Failure, error) {
	fs := []*Failure{}
	testingStarted := false
	section := ""
	sectionHeaderFailures := 0 // # failures at section start
	unknown := []string{}

	// Canonicalize line endings. Note that some logs have a mix
	// of line endings and some somehow have multiple \r's.
	m = canonLine.ReplaceAllString(m, "\n")

	var s []string
	matcher := newMatcher(m)
	consume := func(r *regexp.Regexp) bool {
		matched := matcher.consume(r)
		s = matcher.groups
		if matched && !strings.HasSuffix(s[0], "\n") {
			// Consume the rest of the line.
			matcher.line()
		}
		return matched
	}
	firstBadLine := func() string {
		for _, u := range unknown {
			if len(u) > 0 {
				return u
			}
		}
		return ""
	}

	for !matcher.done() {
		isKnown := true
		switch {
		case consume(testingHeader):
			testingStarted = true

		case consume(sectionHeader):
			section = s[1]
			sectionHeaderFailures = len(fs)

		case consume(testingFailed):
			f := &Failure{
				Test:    s[1],
				Package: s[3],
				Message: "unknown testing.T failure",
			}

			// TODO: Can have multiple errors per FAIL:
			// ../fetchlogs/rev/2015-03-24T19:51:21-41f9c43/linux-arm64-canonical

			sError := testingError.FindStringSubmatch(s[2])
			sPanic := testingPanic.FindStringSubmatch(s[2])
			if sError != nil {
				f.Where, f.Message = sError[1], sError[2]
			} else if sPanic != nil {
				f.Where, f.Message = panicWhere(s[2]), sPanic[1]
			}

			fs = append(fs, f)

		case consume(gotestFailed):
			fs = append(fs, &Failure{
				Package: "test/" + s[2],
				Message: firstLine(s[1]),
			})

		case consume(buildFailed):
			// This may have an accompanying compiler
			// crash, but it's interleaved with other "ok"
			// lines, so it's hard to find.
			fs = append(fs, &Failure{
				Message: "build failed",
				Package: s[1],
			})

		case consume(timeoutPanic1):
			fs = append(fs, &Failure{
				Test:    testFromTraceback(s[1]),
				Message: "test timed out",
				Package: s[2],
			})

		case consume(timeoutPanic2):
			tb := strings.Join(unknown, "\n")
			fs = append(fs, &Failure{
				Test:    testFromTraceback(tb),
				Message: "test timed out",
				Package: s[1],
			})

		case matcher.lineHasLiteral(runtimeLiterals...) && consume(runtimeFailed):
			msg := s[1]
			pkg := "testing"
			if strings.Contains(s[0], "fatal error:") {
				pkg = "runtime"
			}
			traceback := consumeTraceback(matcher)
			matcher.consume(runtimeFailedTrailer)
			fs = append(fs, &Failure{
				Package: pkg,
				Message: msg,
				Where:   panicWhere(traceback),
			})

		case consume(apiCheckerFailed):
			fs = append(fs, &Failure{
				Package: "API checker",
				Message: s[1],
			})

		case consume(goodLine):
			// Ignore. Just clear unknown.

		case consume(testingUnknownFailed):
			fs = append(fs, &Failure{
				Package: s[1],
				Message: "unknown failure: " + firstBadLine(),
			})

		case len(fs) == sectionHeaderFailures && consume(miscFailed):
			fs = append(fs, &Failure{
				Package: section,
				Message: "unknown failure: " + firstBadLine(),
			})

		default:
			isKnown = false
			unknown = append(unknown, matcher.line())
		}

		// Clear unknown lines on any known line.
		if isKnown {
			unknown = unknown[:0]
		}
	}

	if len(fs) == 0 && strings.Contains(m, "no space left on device") {
		fs = append(fs, &Failure{
			Message: "build failed (no space left on device)",
		})
	}
	if len(fs) == 0 && coordinatorTimeout.MatchString(m) {
		// all.bash was killed by coordinator.
		fs = append(fs, &Failure{
			Message: "build failed (timed out)",
		})
	}
	if len(fs) == 0 && strings.Contains(m, "Failed to schedule") {
		// Test sharding failed.
		fs = append(fs, &Failure{
			Message: "build failed (failed to schedule)",
		})
	}
	if len(fs) == 0 && strings.Contains(m, "nosplit stack overflow") {
		fs = append(fs, &Failure{
			Message: "build failed (nosplit stack overflow)",
		})
	}

	// If the same (message, where) shows up in more than five
	// packages, it's probably a systemic issue, so collapse it
	// down to one failure with no package.
	type dedup struct {
		packages map[string]bool
		kept     bool
	}
	msgDedup := map[Failure]*dedup{}
	failureMap := map[*Failure]*dedup{}
	maxCount := 0
	for _, f := range fs {
		key := Failure{
			Message: f.canonicalMessage(),
			Where:   f.Where,
		}

		d := msgDedup[key]
		if d == nil {
			d = &dedup{packages: map[string]bool{}}
			msgDedup[key] = d
		}
		d.packages[f.Package] = true
		if len(d.packages) > maxCount {
			maxCount = len(d.packages)
		}
		failureMap[f] = d
	}
	if maxCount >= 5 {
		fsn := []*Failure{}
		for _, f := range fs {
			d := failureMap[f]
			if len(d.packages) < 5 {
				fsn = append(fsn, f)
			} else if !d.kept {
				d.kept = true
				f.Test, f.Package = "", ""
				fsn = append(fsn, f)
			}
		}
		fs = fsn
	}

	// Check if we even got as far as testing. Note that there was
	// a period when we didn't print the "testing" header, so as
	// long as we found failures, we don't care if we found the
	// header.
	if !testingStarted && len(fs) == 0 {
		fs = append(fs, &Failure{
			Message: "toolchain build failed",
		})
	}

	for _, f := range fs {
		f.OS, f.Arch = os, arch

		// Clean up package. For misc/cgo tests, this will be
		// something like
		// _/tmp/buildlet-scatch825855615/go/misc/cgo/test.
		if strings.HasPrefix(f.Package, "_/tmp/") {
			f.Package = strings.SplitN(f.Package, "/", 4)[3]
		}
	}
	return fs, nil
}

// firstLine returns the first line from s, not including the line
// terminator.
func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

var (
	tracebackStart = regexp.MustCompile(`^(goroutine [0-9]+.*:|runtime stack:)\n`)
	tracebackEntry = regexp.MustCompile(`^` + tbEntry)
)

// consumeTraceback consumes a traceback from m.
func consumeTraceback(m *matcher) string {
	// Find the beginning of the traceback.
	for !m.done() && !m.peek(tracebackStart) {
		m.line()
	}

	start := m.pos
loop:
	for !m.done() {
		switch {
		case m.hasPrefix("\n") || m.hasPrefix("\t") ||
			m.hasPrefix("goroutine ") || m.hasPrefix("runtime stack:") ||
			m.hasPrefix("created by "):
			m.line()

		case m.consume(tracebackEntry):
			// Do nothing.

		default:
			break loop
		}
	}
	return m.str[start:m.pos]
}

var (
	// testFromTracebackRe matches a traceback entry from a
	// function named Test* in a file named *_test.go. It ignores
	// "created by" lines.
	testFromTracebackRe = regexp.MustCompile(`\.(Test[^(\n]+)\(.*\n.*_test\.go`)

	panicWhereRe = regexp.MustCompile(`(?m:^)` + tbEntry)
)

// testFromTraceback attempts to return the test name from a
// traceback.
func testFromTraceback(tb string) string {
	s := testFromTracebackRe.FindStringSubmatch(tb)
	if s == nil {
		return ""
	}
	return s[1]
}

// panicWhere attempts to return the fully qualified name of the
// panicking function in traceback tb.
func panicWhere(tb string) string {
	m := matcher{str: tb}
	for m.consume(panicWhereRe) {
		fn := m.groups[1]

		// Ignore functions involved in panic handling.
		if strings.HasPrefix(fn, "runtime.panic") || fn == "runtime.throw" || fn == "runtime.sigpanic" {
			continue
		}
		return fn
	}
	return ""
}
