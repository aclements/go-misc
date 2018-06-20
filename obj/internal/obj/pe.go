// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package obj

import (
	"debug/pe"
	"fmt"
	"io"
	"sort"
)

type peFile struct {
	pe        *pe.File
	imageBase uint64
}

func openPE(r io.ReaderAt) (Obj, error) {
	f, err := pe.NewFile(r)
	if err != nil {
		return nil, err
	}

	var imageBase uint64
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		imageBase = uint64(oh.ImageBase)
	case *pe.OptionalHeader64:
		imageBase = oh.ImageBase
	default:
		return nil, fmt.Errorf("PE header has unexpected type")
	}

	return &peFile{f, imageBase}, nil
}

func (f *peFile) Symbols() ([]Sym, error) {
	const (
		IMAGE_SYM_UNDEFINED = 0
		IMAGE_SYM_ABSOLUTE  = -1
		IMAGE_SYM_DEBUG     = -2

		IMAGE_SYM_CLASS_STATIC = 3

		IMAGE_SCN_CNT_CODE               = 0x20
		IMAGE_SCN_CNT_INITIALIZED_DATA   = 0x40
		IMAGE_SCN_CNT_UNINITIALIZED_DATA = 0x80
		IMAGE_SCN_MEM_WRITE              = 0x80000000
	)

	var out []Sym
	for _, s := range f.pe.Symbols {
		sym := Sym{s.Name, uint64(s.Value), 0, SymUnknown, false, int(s.SectionNumber)}
		switch s.SectionNumber {
		case IMAGE_SYM_UNDEFINED:
			sym.Kind = SymUndef
		case IMAGE_SYM_ABSOLUTE, IMAGE_SYM_DEBUG:
			// Leave unknown
		default:
			if int(s.SectionNumber)-1 < 0 || int(s.SectionNumber)-1 >= len(f.pe.Sections) {
				// Ignore symbol.
				continue
			}
			sect := f.pe.Sections[int(s.SectionNumber)-1]
			c := sect.Characteristics
			switch {
			case c&IMAGE_SCN_CNT_CODE != 0:
				sym.Kind = SymText
			case c&IMAGE_SCN_CNT_INITIALIZED_DATA != 0:
				if c&IMAGE_SCN_MEM_WRITE != 0 {
					sym.Kind = SymData
				} else {
					sym.Kind = SymROData
				}
			case c&IMAGE_SCN_CNT_UNINITIALIZED_DATA != 0:
				sym.Kind = SymBSS
			}
			sym.Local = s.StorageClass == IMAGE_SYM_CLASS_STATIC
			sym.Value += f.imageBase + uint64(sect.VirtualAddress)
		}

		out = append(out, sym)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	for i := range out {
		sym1 := &out[i]
		if i+1 < len(out) {
			sym2 := out[i+1]
			if sym1.section == sym2.section {
				sym1.Size = sym2.Value - sym1.Value
				continue
			}
		}
		// Symbol is the last in its section.
		sect := f.pe.Sections[sym1.section-1]
		sym1.Size = uint64(sect.VirtualAddress) + uint64(sect.VirtualSize) - sym1.Value
	}

	return out, nil
}

func (f *peFile) SymbolData(s Sym) ([]byte, error) {
	if s.section <= 0 || s.section-1 >= len(f.pe.Sections) {
		return nil, nil
	}
	sect := f.pe.Sections[s.section-1]
	if s.Value < uint64(sect.VirtualAddress) {
		return nil, fmt.Errorf("symbol %q starts before section %q", s.Name, sect.Name)
	}
	out := make([]byte, s.Size)
	pos := s.Value - (f.imageBase + uint64(sect.VirtualAddress))
	if pos >= uint64(sect.Size) {
		return out, nil
	}
	flen := s.Size
	if flen > uint64(sect.Size)-pos {
		flen = uint64(sect.Size) - pos
	}
	_, err := sect.ReadAt(out[:flen], int64(pos))
	return out, err
}
