// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Defensively block building on untested versions:
// +build go1.8,!go1.12

// Package split provides a logical value type that is split across
// one or more shards to achieve better parallelism.
//
// Split values have many uses, but are primarily for optimizing
// "write-mostly" shared data structures that have commutative
// operations. Split values allow concurrent updates to happen on
// different shards, which minimizes contention between updates.
// However, reading the entire value requires combining all of these
// shards, which is a potentially expensive operation.
//
// WARNING: This package depends on Go runtime internals. It has been
// tested with Go 1.8 through Go 1.10, but may not work with older or
// newer versions.
package split

import (
	"fmt"
	"reflect"
	"runtime"
	"unsafe"
)

const cacheLineBytes = 128

// Value represents a logical value split across one or more shards.
// The shards are arranged to minimize contention when different
// shards are accessed concurrently.
type Value struct {
	store     unsafe.Pointer
	ptrType   unsafe.Pointer
	shardSize uintptr
	len       int
	cbType    reflect.Type
}

type emptyInterface struct {
	typ  unsafe.Pointer
	word unsafe.Pointer
}

// New returns a new Value. The constructor argument must be a
// function with type func(*T), where T determines the type that will
// be stored in each shard. New will initialize each shard to the zero
// value of T and then call constructor with a pointer to the shard to
// perform any further initialization. The constructor function may
// also be called in the future if new shards are created.
func New(constructor interface{}) *Value {
	ct := reflect.TypeOf(constructor)
	if ct.Kind() != reflect.Func || ct.NumIn() != 1 || ct.NumOut() != 0 || ct.In(0).Kind() != reflect.Ptr {
		panic("New constructor must be func(*T) for some type T")
	}
	et := ct.In(0).Elem()

	// Embed et in a struct so we can pad it out to a cache line.
	//
	// TODO: If et is small, this can stride-allocate multiple
	// Values together. Would need non-trivial runtime support,
	// but would save a lot of space. We could do this for
	// pointer-free types without runtime support and maybe types
	// that are just a pointer.
	shardSize := (et.Size() + (cacheLineBytes - 1)) &^ (cacheLineBytes - 1)
	padding := shardSize - et.Size()
	padded := reflect.StructOf([]reflect.StructField{
		{Name: "X", Type: et},
		{Name: "Pad", Type: reflect.ArrayOf(int(padding), byteType)},
	})

	// Allocate backing store.
	nproc := runtime.GOMAXPROCS(-1)
	store := reflect.New(reflect.ArrayOf(nproc, padded))

	// Get pointer-to-element type.
	pet := reflect.PtrTo(et)
	petz := reflect.Zero(pet).Interface()
	ptrType := (*emptyInterface)(unsafe.Pointer(&petz)).typ

	v := &Value{
		store:     unsafe.Pointer(store.Pointer()),
		ptrType:   ptrType,
		shardSize: shardSize,
		len:       nproc,
		cbType:    ct, // func(T*) type, same as constructor.
	}

	// Initialize each shard.
	v.Range(constructor)

	return v
}

var byteType = reflect.TypeOf(byte(0))

// Get returns a pointer to some shard of v.
//
// Get may return the same pointer to multiple goroutines, so the
// caller is responsible for synchronizing concurrent access to the
// returned value. This can be done using atomic operations or locks,
// just like any other shared structure.
//
// Get attempts to maintain CPU locality and contention-freedom of
// shards. That is, two calls to Get from the same CPU are likely to
// return the same pointer, while calls to Get from different CPUs are
// likely to return different pointers. Furthermore, accessing
// different shards in parallel is unlikely to result in cache
// contention.
func (v *Value) Get() interface{} {
	// Get the P ID.
	//
	// TODO: Could use CPU ID instead of P ID. Would get even
	// better cache locality and limit might be more fixed.
	//
	// TODO: We don't need pinning here.
	pid := runtime_procPin()
	runtime_procUnpin()

	// This is 10% faster than procPin/procUnpin. It requires the
	// following patch to the runtime:
	////go:linkname sync_split_procID sync/split.procID
	//func sync_split_procID() int {
	//	return int(getg().m.p.ptr().id)
	//}
	//pid := procID()

	// This is 30% faster than procPin/procUnpin. It requires the
	// following patch to the runtime:
	//func ProcID() int {
	//	return int(getg().m.p.ptr().id)
	//}
	// However, it's unclear how to do this without exposing public API.
	//pid := runtime.ProcID()

	if pid > v.len {
		// TODO: Grow the backing store if pid is larger than
		// store. This is tricky because we may have handed
		// out pointers into the current store. Probably this
		// is only possible with a level of indirection that
		// lets us allocate the backing store in multiple
		// segments. Then we can do an RCU-style update on the
		// index structure. We may want to limit the number of
		// shards to something sane anyway (e.g., 1024). How
		// would this synchronize with Range? E.g., if Range
		// iterator is going through locking everything, it
		// would be bad if Get then made a new, unlocked
		// element.
		pid = int(uint(pid) % uint(v.len))
	}
	val := emptyInterface{
		typ:  v.ptrType,
		word: v.shard(pid),
	}
	return *(*interface{})(unsafe.Pointer(&val))
}

func (v *Value) shard(shard int) unsafe.Pointer {
	// The caller must ensure that 0 <= shard < v.len.
	return unsafe.Pointer(uintptr(v.store) + v.shardSize*uintptr(shard))
}

// Range calls each of its argument functions with pointers to all of
// the shards in v. Each argument must be a function with type
// func(*T), where T is the shard type of the Value.
//
// Range calls its first argument N times with a pointer to each of
// the N shards of v. It then calls its second argument with each
// shard, and so on. Range guarantees that the set of shards and their
// order will not change during this process. This makes it safe to
// implement multi-pass algorithms, such as locking all of the shards
// and then unlocking all of the shards.
//
// Multiple calls to Range are not guaranteed to observe the same set
// of shards, so algorithms that need a consistent view of the shards
// must make a single call to Range with multiple functions.
//
// Multiple calls to Range are guaranteed to traverse the shards in a
// consistent order. While different calls may traverse more or fewer
// shards, if any Range traverses shard A before shard B, all Range
// calls will do so. Uses of Range that acquire locks on multiple
// shards can depend on this for lock ordering.
//
// Range calls each function sequentially, so it's safe to update
// local state without synchronization. However, the functions may run
// concurrently with other goroutines calling Get or Range, so they
// must synchronize access to shard values.
func (v *Value) Range(fn ...interface{}) {
	// "Type check" all of the fn arguments before calling
	// anything.
	//
	// TODO: Accept any func(U) where *T is assignable to U (like
	// runtime.SetFinalizer).
	for _, fn1 := range fn {
		if reflect.TypeOf(fn1) != v.cbType {
			panic(fmt.Sprintf("Range expected %s, got %T", v.cbType, fn1))
		}
	}

	// TODO: If we grow the backing store, this needs to block
	// growing if there are multiple passes (it doesn't have to if
	// there's one pass, but it has to handle it very carefully).
	for _, fn1 := range fn {
		// Cast fn1 to a function with equivalent calling
		// convention.
		var fn1Generic func(unsafe.Pointer)
		*(*unsafe.Pointer)(unsafe.Pointer(&fn1Generic)) = ((*emptyInterface)(unsafe.Pointer(&fn1)).word)
		// Call function on each shard.
		for i := 0; i < v.len; i++ {
			fn1Generic(v.shard(i))
		}
	}
}

//go:linkname runtime_procPin runtime.procPin
func runtime_procPin() int

//go:linkname runtime_procUnpin runtime.procUnpin
func runtime_procUnpin()

// Provided by the runtime (with patch above).
func procID() int
