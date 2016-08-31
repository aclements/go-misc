// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

import (
	"bytes"
	"fmt"
)

type traceEntry struct {
	tid int
	msg string
}

func (s *Scheduler) Trace(msg string) {
	s.trace = append(s.trace, traceEntry{s.curThread.id, msg})
}

func (s *Scheduler) Tracef(msg string, args ...interface{}) {
	s.trace = append(s.trace, traceEntry{s.curThread.id, fmt.Sprintf(msg, args...)})
}

type errorWithTrace struct {
	err   interface{}
	trace []traceEntry
}

func (e errorWithTrace) Error() string {
	if len(e.trace) == 0 {
		return fmt.Sprint(e.err)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%v\n", e.err)
	fmt.Fprintf(&buf, "trace:")
	for _, ent := range e.trace {
		fmt.Fprintf(&buf, "\n  T%d %s", ent.tid, ent.msg)
	}
	return buf.String()
}
