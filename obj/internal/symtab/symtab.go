// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package symtab

import (
	"sort"

	"github.com/aclements/go-misc/obj/internal/obj"
)

// Table facilitates fast symbol lookup.
type Table struct {
	addr []obj.Sym
	name map[string]int
}

// NewTable creates a new table for syms.
func NewTable(syms []obj.Sym) *Table {
	// Put syms in address order for fast address lookup.
	sort.Slice(syms, func(i, j int) bool {
		return syms[i].Value < syms[j].Value
	})

	// Create name map for fast name lookup.
	name := make(map[string]int)
	for i, s := range syms {
		name[s.Name] = i
	}

	return &Table{syms, name}
}

// Syms returns all symbols in Table in address order. The caller must
// not modify the returned slice.
func (t *Table) Syms() []obj.Sym {
	return t.addr
}

// Name returns the symbol with the given name.
func (t *Table) Name(name string) (obj.Sym, bool) {
	if i, ok := t.name[name]; ok {
		return t.addr[i], true
	}
	return obj.Sym{}, false
}

// Addr returns the symbol containing addr.
func (t *Table) Addr(addr uint64) (obj.Sym, bool) {
	i := sort.Search(len(t.addr), func(i int) bool {
		return addr < t.addr[i].Value
	})
	if i > 0 {
		s := t.addr[i-1]
		if s.Value != 0 && s.Value <= addr && addr < s.Value+s.Size {
			return s, true
		}
	}
	return obj.Sym{}, false
}

// SymName returns the name and base of the symbol containing addr. It
// returns "", 0 if no symbol contains addr.
//
// This is useful for x/arch disassembly functions.
func (t *Table) SymName(addr uint64) (name string, base uint64) {
	if sym, ok := t.Addr(addr); ok {
		return sym.Name, sym.Value
	}
	return "", 0
}
