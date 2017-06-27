// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

type AtomicInt32 struct {
	v int32
}

func (a *AtomicInt32) Add(delta int32) (new int32) {
	a.v += delta
	new = a.v
	globalSched.Sched()
	return new
}

func (a *AtomicInt32) CompareAndSwap(old, new int32) (swapped bool) {
	swapped = a.v == old
	if swapped {
		a.v = new
	}
	globalSched.Sched()
	return
}

func (a *AtomicInt32) Load() int32 {
	v := a.v
	globalSched.Sched()
	return v
}

func (a *AtomicInt32) Store(val int32) {
	a.v = val
	globalSched.Sched()
}

func (a *AtomicInt32) Swap(new int32) (old int32) {
	old, a.v = a.v, new
	globalSched.Sched()
	return
}
