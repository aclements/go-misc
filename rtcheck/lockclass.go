// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// A LockClass represents a set of locks. LockClasses form equivalence
// classes: a given lock instance belongs to exactly one lock class.
// Often, a LockClass is identified by the struct type and field that
// embeds the lock object. It may also simply be a package-level
// variable.
type LockClass struct {
	label    string
	isUnique bool
	id       int
	lca      *LockClassAnalysis
}

func (lc *LockClass) Analysis() *LockClassAnalysis {
	return lc.lca
}

func (lc *LockClass) String() string {
	if !lc.isUnique {
		return lc.label + "*"
	}
	return lc.label
}

// IsUnique returns true if lc is inhabited by a single lock instance.
func (lc *LockClass) IsUnique() bool {
	return lc.isUnique
}

// Id returns a small integer ID for this lock class that is unique
// within the LockClassAnalysis that returned this *LockClass.
func (lc *LockClass) Id() int {
	return lc.id
}

type lockClassKey struct {
	parent interface{}
	field  int
	global *ssa.Global
	typ    *types.Named
}

type LockClassAnalysis struct {
	classes map[lockClassKey]*LockClass
	list    []*LockClass
}

// Get returns the LockClass of the given ssa.Value, which must be a
// pointer to a global or a field address expression. If Get cannot
// resolve the lock class of v, it returns an error indicating why.
//
// The returned pointer uniquely identifies the lock class. That is,
// a.Get(x) == a.Get(y) if and only if x and y are the same lock
// class.
func (a *LockClassAnalysis) Get(v ssa.Value) (*LockClass, error) {
	// Strip away FieldAddrs until we get to something that's a
	// global or a *struct value.
	label := make([]string, 0, 10)
	var key lockClassKey
	var isUnique bool
loop:
	for {
		switch v2 := v.(type) {
		case *ssa.FieldAddr:
			// TODO: How does this handle nested structs?
			label = append(label, v2.X.Type().Underlying().(*types.Pointer).Elem().Underlying().(*types.Struct).Field(v2.Field).Name())
			key = lockClassKey{parent: key, field: v2.Field}
			v = v2.X

		case *ssa.Global:
			// TODO: Check formatting
			label = append(label, v2.String())
			key = lockClassKey{parent: key, global: v2}
			isUnique = true
			break loop

		default:
			if len(label) == 0 {
				// This wasn't a field of something,
				// so we have no idea what its lock
				// class is. This happens in the
				// runtime for example in
				// parkunlock_c, which cases an
				// unsafe.Pointer argument to a
				// *mutex.
				return nil, fmt.Errorf("lock is not a field or global")
			}
			// This must be a *struct. Get the struct's
			// name.
			styp, ok := v.Type().Underlying().(*types.Pointer).Elem().(*types.Named)
			if !ok {
				return nil, fmt.Errorf("lock is a field of an unnamed struct")
			}
			sname := styp.Obj().Name()
			label = append(label, styp.Obj().Pkg().Name()+"."+sname)
			key = lockClassKey{parent: key, typ: styp}
			isUnique = false
			break loop
		}
	}

	if a.classes == nil {
		a.classes = make(map[lockClassKey]*LockClass)
	}
	if lc, ok := a.classes[key]; ok {
		return lc, nil
	}

	for i := 0; i < len(label)/2; i++ {
		label[i], label[len(label)-i-1] = label[len(label)-i-1], label[i]
	}
	lc := &LockClass{
		label:    strings.Join(label, "."),
		isUnique: isUnique,
		id:       len(a.list),
		lca:      a,
	}
	a.classes[key] = lc
	a.list = append(a.list, lc)
	return lc, nil
}

// NewLockClass returns a new lock class that is distinct from every
// other lock class. This can be used to model lock-like abstractions
// that are not actually Go objects.
func (a *LockClassAnalysis) NewLockClass(label string, isUnique bool) *LockClass {
	lc := &LockClass{
		label:    label,
		isUnique: isUnique,
		id:       len(a.list),
		lca:      a,
	}
	a.list = append(a.list, lc)
	return lc
}

// Lookup returns the *LockClass whose Id() is id.
func (a *LockClassAnalysis) Lookup(id int) *LockClass {
	return a.list[id]
}
