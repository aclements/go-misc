package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
)

const pageSize = 4096

var smapsRe = regexp.MustCompile("^([0-9a-f]+)-([0-9a-f]+) ")

func main() {
	pid, err := strconv.Atoi(os.Args[1])
	if err != nil {
		log.Fatal("bad pid")
	}

	smaps, err := os.Open(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		log.Fatal(err)
	}

	pagemap, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		log.Fatal(err)
	}

	pageflags, err := os.Open("/proc/kpageflags")
	if err != nil {
		log.Fatal(err)
	}

	scanner := bufio.NewScanner(smaps)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)

		s := smapsRe.FindStringSubmatch(line)
		if s == nil {
			continue
		}
		lo, _ := strconv.ParseUint(s[1], 16, 64)
		hi, _ := strconv.ParseUint(s[2], 16, 64)

		dumpRange(pagemap, pageflags, lo, hi)
	}
	if err := scanner.Err(); err != nil {
		log.Fatal("reading smaps: ", err)
	}
}

const (
	PAGEFLAG_ANON          = 1 << 12
	PAGEFLAG_COMPOUND_HEAD = 1 << 15
	PAGEFLAG_COMPOUND_TAIL = 1 << 16
	PAGEFLAG_HUGE          = 1 << 17
	PAGEFLAG_THP           = 1 << 22
	PAGEFLAG_ZERO_PAGE     = 1 << 24
)

type pageFlags uint64

func (p pageFlags) String() string {
	s := ""
	if p&PAGEFLAG_ANON != 0 {
		s += "ANON|"
		p &^= PAGEFLAG_ANON
	}
	if p&PAGEFLAG_COMPOUND_HEAD != 0 {
		s += "COMPOUND_HEAD|"
		p &^= PAGEFLAG_COMPOUND_HEAD
	}
	if p&PAGEFLAG_COMPOUND_TAIL != 0 {
		s += "COMPOUND_TAIL|"
		p &^= PAGEFLAG_COMPOUND_TAIL
	}
	if p&PAGEFLAG_HUGE != 0 {
		s += "HUGE|"
		p &^= PAGEFLAG_HUGE
	}
	if p&PAGEFLAG_THP != 0 {
		s += "THP|"
		p &^= PAGEFLAG_THP
	}
	if p&PAGEFLAG_ZERO_PAGE != 0 {
		s += "ZERO_PAGE|"
		p &^= PAGEFLAG_ZERO_PAGE
	}
	if p != 0 {
		return fmt.Sprintf("%s%#x", s, uint64(p))
	}
	if s == "" {
		return "0"
	}
	return s[:len(s)-1]
}

func dumpRange(pagemap, pageflags *os.File, lo, hi uint64) {
	const batch = 1024
	buf := make([]byte, 8*batch)
	for addr := lo; addr < hi; addr += pageSize * batch {
		if int(8*(hi-addr)/pageSize) < len(buf) {
			buf = buf[:8*(hi-addr)/pageSize]
		}
		_, err := pagemap.ReadAt(buf, 8*int64(addr/pageSize))
		if err != nil {
			log.Fatal("reading pagemap: ", err)
		}

		// Decode pages in buf.
		for i := 0; i < len(buf)/8; i++ {
			pageinfo := binary.LittleEndian.Uint64(buf[i*8:])
			if pageinfo&(1<<63) == 0 {
				// Not present.
				continue
			}
			// Bits 0--54 are the PFN if present.
			pfn := pageinfo & (1<<55 - 1)

			// Look up PFN in pageflags.
			var flagbuf [8]byte
			_, err := pageflags.ReadAt(flagbuf[:], int64(8*pfn))
			if err != nil {
				log.Fatal("reading pageflags: ", err)
			}
			flags := binary.LittleEndian.Uint64(flagbuf[:])

			fmt.Printf("%016x %08x %s\n", addr+uint64(i*pageSize), pfn, pageFlags(flags)) // XXX
			if flags&PAGEFLAG_THP != 0 && flags&PAGEFLAG_COMPOUND_HEAD != 0 {
				// Head of a transparent huge page.
				i += 511
			}
		}
	}
}
