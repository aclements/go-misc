// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package obj

import (
	"debug/elf"
	"fmt"
	"io"
)

type elfFile struct {
	elf *elf.File
}

func openElf(r io.ReaderAt) (Obj, error) {
	f, err := elf.NewFile(r)
	if err != nil {
		return nil, err
	}
	return &elfFile{f}, nil
}

func (f *elfFile) Symbols() ([]Sym, error) {
	syms, err := f.elf.Symbols()
	if err != nil {
		return nil, err
	}

	var out []Sym
	for _, s := range syms {
		kind := SymUnknown
		switch s.Section {
		case elf.SHN_UNDEF:
			kind = SymUndef
		case elf.SHN_COMMON:
			kind = SymBSS
		default:
			if s.Section < 0 || s.Section >= elf.SectionIndex(len(f.elf.Sections)) {
				// Ignore symbol.
				continue
			}
			sect := f.elf.Sections[s.Section]
			switch sect.Flags & (elf.SHF_WRITE | elf.SHF_ALLOC | elf.SHF_EXECINSTR) {
			case elf.SHF_ALLOC | elf.SHF_EXECINSTR:
				kind = SymText
			case elf.SHF_ALLOC:
				kind = SymROData
			case elf.SHF_ALLOC | elf.SHF_WRITE:
				kind = SymData
			}
		}
		local := elf.ST_BIND(s.Info) == elf.STB_LOCAL

		sym := Sym{s.Name, s.Value, s.Size, kind, local, int(s.Section)}
		out = append(out, sym)
	}
	return out, nil
}

func (f *elfFile) SymbolData(s Sym) ([]byte, error) {
	sect := f.elf.Sections[s.section]
	out := make([]byte, s.Size)
	if s.Value < sect.Addr {
		return nil, fmt.Errorf("symbol %q starts before section %q", s.Name, sect.Name)
	}
	pos := s.Value - sect.Addr
	if pos >= sect.Size {
		return out, nil
	}
	flen := s.Size
	if flen > sect.Size-pos {
		flen = sect.Size - pos
	}
	_, err := sect.ReadAt(out[:flen], int64(pos))
	return out, err
}
