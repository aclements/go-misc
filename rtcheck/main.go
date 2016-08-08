// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command rtcheck performs static analysis of the Go runtime.
//
// Note: Currently this requires a small modification to
// golang.org/x/tools/go/pointer:
//
//     --- a/go/pointer/intrinsics.go
//     +++ b/go/pointer/intrinsics.go
//     @@ -180,7 +180,6 @@ func (a *analysis) findIntrinsic(fn *ssa.Function) intrinsic {
//      			// Ignore "runtime" (except SetFinalizer):
//      			// it has few interesting effects on aliasing
//      			// and is full of unsafe code we can't analyze.
//     -			impl = ext۰NoEffect
//      		}
//
//      		a.intrinsics[fn] = impl
//
// rtcheck currently implements one analysis:
//
// Deadlock detection
//
// Static deadlock detection constructs a lock graph and reports
// cycles in that lock graph. These cycles indicate code paths with
// the potential for deadlock.
//
// The report from the deadlock detector indicates all discovered
// cycles in the lock graph and, for each edge L1 -> L2, shows the
// code paths that acquire L2 while holding L1. In the simplest case
// where L1 and L2 are the same lock, this cycle represents a
// potential for self-deadlock within a single thread. More generally,
// it means that if all of the code paths in the cycle execute
// concurrently, the system may deadlock. If one of the edges in a
// cycle is represented by significantly fewer code paths than the
// other edges, fixing those code paths is likely the easiest way to
// fix the deadlock.
//
// This uses an inter-procedural, path-sensitive, and partially
// value-sensitive analysis based on Engler and Ashcroft, "RacerX:
// Effective, static detection of race conditions and deadlocks", SOSP
// 2003. It works by exploring possible code paths and finding paths
// on which two or more locks are held simultaneously. Any such path
// produces one or more edges in the lock graph indicating the order
// in which those locks were acquired.
//
// Like many static analyses, this has limitations. First, it doesn't
// reason about individual locks, but about lock *classes*, which are
// modeled as sets of locks that may alias each other. As a result, if
// the code acquires multiple locks from the same lock class
// simultaneously (such as locks from different instances of the same
// structure), but is careful to ensure a consistent order between
// those locks at runtime (e.g., by sorting them), this analysis will
// consider that a potential deadlock, even though it will not
// deadlock at runtime.
//
// Second, it may explore code paths that are impossible at runtime.
// The analysis performs very simple intra-procedural value
// propagation to eliminate obviously impossible code paths, but this
// is easily fooled. Consider
//
//     if complex condition 1 {
//         lock(&x)
//     }
//     ...
//     if complex condition 2 {
//         unlock(&x)
//     }
//
// where complex conditions 1 and 2 are equivalent, but beyond the
// reach of the simple value propagation. The analysis will see *four*
// distinct code paths here, rather than the two that are actually
// possible, and think that x can still be held after the second if.
// Similarly,
//
//     lock(&x)
//     ensure !c
//     ...
//     if c {
//         lock(&x)
//     }
//
// If c can't be deduced by value propagation, this will appear as a
// potential self-deadlock. Of course, if it requires complex dynamic
// reasoning to show that a deadlock cannot occur at runtime, it may
// be a good idea to simplify the code anyway.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// debugFunctions is a set of functions to enable extra debugging
// tracing for. Each function in debugFunctions will generate a dot
// file containing the block exploration graph of that function.
var debugFunctions = map[string]bool{}

func main() {
	var (
		outLockGraph string
		outCallGraph string
		outHTML      string
		debugFuncs   string
	)
	flag.StringVar(&outLockGraph, "lockgraph", "", "write lock graph in dot to `file`")
	flag.StringVar(&outCallGraph, "callgraph", "", "write call graph in dot to `file`")
	flag.StringVar(&outHTML, "html", "", "write HTML deadlock report to `file`")
	flag.StringVar(&debugFuncs, "debugfuncs", "", "write debug graphs for `funcs` (comma-separated list)")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	for _, name := range strings.Split(debugFuncs, ",") {
		debugFunctions[name] = true
	}

	var conf loader.Config

	// TODO: Check all reasonable arch/OS combos.

	// TODO: This would be so much easier and nicer if I could
	// just plug (path, AST)s into the loader, or at least slip in
	// between when the loader has parsed everything and when it
	// type-checks everything. Currently it's only possible to
	// provide ASTs for non-importable packages to the
	// loader.Config.

	newSources := make(map[string][]byte)
	for _, pkgName := range []string{"runtime", "runtime/internal/atomic"} {
		buildPkg, err := build.Import(pkgName, "", 0)
		if err != nil {
			log.Fatal(err)
		}
		rewriteSources(buildPkg, newSources)
	}

	ctxt := &build.Default
	ctxt = buildutil.OverlayContext(ctxt, newSources)

	conf.Build = ctxt
	conf.Import("runtime")

	lprog, err := conf.Load()
	if err != nil {
		log.Fatal("loading runtime: ", err)
	}
	fset := lprog.Fset

	prog := ssautil.CreateProgram(lprog, 0)
	prog.Build()
	runtimePkg := prog.ImportedPackage("runtime")

	// TODO: Teach it that you can jump to sigprof at any point?
	//
	// TODO: Teach it about implicit write barriers?
	//
	// TODO: Teach it about morestack at every function entry?

	// Prepare for pointer analysis.
	ptrConfig := pointer.Config{
		Mains:          []*ssa.Package{runtimePkg},
		BuildCallGraph: true,
		//Log:            os.Stderr,
	}

	// Register arguments to runtime.lock/unlock for PTA.
	registerLockQueries(runtimePkg, &ptrConfig)

	// Run pointer analysis.
	pta, err := pointer.Analyze(&ptrConfig)
	if err != nil {
		log.Fatal(err)
	}
	cg := pta.CallGraph

	cg.DeleteSyntheticNodes() // ?

	// Output call graph if requested.
	if outCallGraph != "" {
		withWriter(outCallGraph, func(w io.Writer) {
			type edge struct{ a, b *callgraph.Node }
			have := make(map[edge]struct{})
			fmt.Fprintln(w, "digraph callgraph {")
			callgraph.GraphVisitEdges(pta.CallGraph, func(e *callgraph.Edge) error {
				if _, ok := have[edge{e.Caller, e.Callee}]; ok {
					return nil
				}
				have[edge{e.Caller, e.Callee}] = struct{}{}
				fmt.Fprintf(w, "%q -> %q;\n", e.Caller.Func, e.Callee.Func)
				return nil
			})
			fmt.Fprintln(w, "}")
		})
	}

	stringSpace := NewStringSpace()
	s := state{
		fset: fset,
		cg:   cg,
		pta:  pta,
		fns:  make(map[*ssa.Function]*funcInfo),

		lockOrder: NewLockOrder(fset),

		roots:   nil,
		rootSet: make(map[*ssa.Function]struct{}),
	}
	// TODO: Add roots from
	// cmd/compile/internal/gc/builtin/runtime.go. Will need to
	// add them as roots for PTA too. Maybe just synthesize a main
	// function that calls all of them and use that as the root
	// here.
	for _, name := range []string{"newobject"} {
		m := runtimePkg.Members[name].(*ssa.Function)
		s.addRoot(m)
	}
	for i := 0; i < len(s.roots); i++ {
		root := s.roots[i]
		exitLockSets := s.walkFunction(root, NewLockSet(stringSpace))
		// Warn if any locks are held at return.
		for _, ls := range exitLockSets.M {
			if len(ls.stacks) == 0 {
				continue
			}
			s.warnl(root.Pos(), "locks at return from root %s: %s", root, exitLockSets)
			s.warnl(root.Pos(), "\t(likely analysis failed to match control flow for unlock)\n")
		}
	}

	// Dump debug trees.
	if s.debugTree != nil {
		withWriter("debug-functions.dot", s.debugTree.WriteToDot)
	}
	for fn, fInfo := range s.fns {
		if fInfo.debugTree == nil {
			continue
		}
		withWriter(fmt.Sprintf("debug-%s.dot", fn), fInfo.debugTree.WriteToDot)
	}

	// Output lock graph.
	if outLockGraph != "" {
		withWriter(outLockGraph, s.lockOrder.WriteToDot)
	}

	// Output HTML report.
	if outHTML != "" {
		withWriter(outHTML, s.lockOrder.WriteToHTML)
	}

	// Output text lock cycle report.
	fmt.Println()
	fmt.Print("roots:")
	for _, fn := range s.roots {
		fmt.Printf(" %s", fn)
	}
	fmt.Print("\n")
	fmt.Printf("number of lock cycles: %d\n\n", len(s.lockOrder.FindCycles()))
	s.lockOrder.Check(os.Stdout)
}

// withWriter creates path and calls f with the file.
func withWriter(path string, f func(w io.Writer)) {
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	f(file)
}

// rewriteSources rewrites all of the Go files in pkg to eliminate
// runtime-isms and make them easier for go/ssa to process. It fills
// rewritten with path -> new source mappings.
func rewriteSources(pkg *build.Package, rewritten map[string][]byte) {
	for _, fname := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, fname)

		// Parse source.
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			log.Fatalf("%s: %s", path, err)
		}

		rewriteStubs(f)
		if pkg.Name == "runtime" {
			rewriteRuntime(f)
		}

		// Back to source.
		var buf bytes.Buffer
		if err := (&printer.Config{Mode: printer.SourcePos, Tabwidth: 8}).Fprint(&buf, fset, f); err != nil {
			log.Fatalf("outputting replacement %s: %s", path, err)
		}

		if pkg.Name == "runtime" && fname == "stubs.go" {
			// Add calls to runtime roots for PTA.
			buf.Write([]byte(`
var _ = newobject(nil)
`))
		}

		rewritten[path] = buf.Bytes()
	}
}

var newStubs = make(map[string]map[string]*ast.FuncDecl)

func init() {
	var runtimeStubs = `
package runtime

// stubs.go
func getg() *g { return nil }
// Not mcall or systemstack
func memclr() { }
func memmove() { }
func fastrand1() uint32 { return 0 }
func memequal() bool { return false }
func noescape(p unsafe.Pointer) unsafe.Pointer { return p }
func cgocallback() { }
func gogo() { for { } }
func gosave() { }
func mincore() int32 { return 0 }
func jmpdefer() { for { } }
func exit1() { for { } }
func setg() { }
func breakpoint() { }
func reflectcall() { }
func procyield() { }
func cgocallback_gofunc() { }
func publicationBarrier() { }
func setcallerpc() { }
func getcallerpc() uintptr { return 0 }
func getcallersp() uintptr { return 0 }
func asmcgocall() int32 { return 0 }
func morestack() { newstack() }
func time_now() (int64, int32) { return 0, 0 }

// os_linux.go
func futex() int32 { return 0 }
func clone() int32 { return 0 }
func gettid() uint32 { return 0 }
func sigreturn() { for { } }
func rt_sigaction() int32 { return 0 }
func sigaltstack() { }
func setitimer() { }
func rtsigprocmask() { }
func getrlimit() int32 { return 0 }
func raise() { for { } }
func raiseproc() { for { } }
func sched_getaffinity() int32 { return 0 }
func osyield() { }

// stubs2.go
func read() { return 0 }
func closefd() { return 0 }
func exit() { for {} }
func nanotime() { return 0 }
func usleep() {}
func munmap() {}
func write() int32 { return 0 }
func open() int32 { return 0 }
func madvise() {}

// cputicks.go
func cputicks() { return 0 }

// cgo_mmap.go
func sysMmap() unsafe.Pointer { return nil }
func callCgoMmap() uintptr { return 0 }

// alg.go
func aeshash(p unsafe.Pointer, h, s uintptr) uintptr { return 0 }
func aeshash32(p unsafe.Pointer, h uintptr) uintptr { return 0 }
func aeshash64(p unsafe.Pointer, h uintptr) uintptr { return 0 }
func aeshashstr(p unsafe.Pointer, h uintptr) uintptr { return 0 }

// netpoll_epoll.go
func epollcreate(size int32) int32 { return 0 }
func epollcreate1(flags int32) int32 { return 0 }
func epollctl(epfd, op, fd int32, ev *epollevent) int32 { return 0 }
func epollwait(epfd int32, ev *epollevent, nev, timeout int32) int32 { return 0 }
func closeonexec(fd int32) {}
`
	var atomicStubs = `
package atomic

// stubs.go
func Cas(ptr *uint32, old, new uint32) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Casp1(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Casuintptr(ptr *uintptr, old, new uintptr) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Storeuintptr(ptr *uintptr, new uintptr) { *ptr = new }
func Loaduintptr(ptr *uintptr) uintptr { return *ptr }
func Loaduint(ptr *uint) uint { return *ptr }
func Loadint64(ptr *int64) int64 { return *ptr }
func Xaddint64(ptr *int64, delta int64) int64 {
	*ptr += delta
	return *ptr
}

// atomic_*.go
func Load(ptr *uint32) uint32 { return *ptr }
func Loadp(ptr unsafe.Pointer) unsafe.Pointer { return *(*unsafe.Pointer)(ptr) }
func Load64(ptr *uint64) uint64 { return *ptr }
func Xadd(ptr *uint32, delta int32) uint32 {
	*ptr += uint32(delta)
	return *ptr
}
func Xadd64(ptr *uint64, delta int64) uint64 {
	*ptr += uint64(delta)
	return *ptr
}
func Xadduintptr(ptr *uintptr, delta uintptr) uintptr {
	*ptr += delta
	return *ptr
}
func Xchg(ptr *uint32, new uint32) uint32 {
	old := *ptr
	*ptr = new
	return old
}
func Xchg64(ptr *uint64, new uint64) uint64 {
	old := *ptr
	*ptr = new
	return old
}
func Xchguintptr(ptr *uintptr, new uintptr) uintptr {
	old := *ptr
	*ptr = new
	return old
}
func And8(ptr *uint8, val uint8) { *ptr &= val }
func Or8(ptr *uint8, val uint8) { *ptr |= val }
func Cas64(ptr *uint64, old, new uint64) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Store(ptr *uint32, val uint32) { *ptr = val }
func Store64(ptr *uint64, val uint64) { *ptr = val }
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer) {
	*(*unsafe.Pointer)(ptr) = val
}
`

	for _, stubs := range []string{runtimeStubs, atomicStubs} {
		f, err := parser.ParseFile(token.NewFileSet(), "<newStubs>", stubs, 0)
		if err != nil {
			log.Fatal("parsing replacement stubs: ", err)
		}

		// Strip token.Pos information from stubs. It confuses
		// the printer, which winds up producing invalid Go code.
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				return true
			}
			rn := reflect.ValueOf(n).Elem()
			for i := 0; i < rn.NumField(); i++ {
				f := rn.Field(i)
				if _, ok := f.Interface().(token.Pos); ok {
					f.Set(reflect.Zero(f.Type()))
				}
			}
			return true
		})

		newMap := make(map[string]*ast.FuncDecl)
		for _, decl := range f.Decls {
			newMap[decl.(*ast.FuncDecl).Name.Name] = decl.(*ast.FuncDecl)
		}
		newStubs[f.Name.Name] = newMap
	}
}

func rewriteStubs(f *ast.File) {
	// Replace declaration bodies.
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body != nil {
				continue
			}
			if newDecl, ok := newStubs[f.Name.Name][decl.Name.Name]; ok {
				decl.Body = newDecl.Body
			}
		}
	}
}

func rewriteRuntime(f *ast.File) {
	// TODO: Rewrite new/make/etc to calls to built-ins.
	Rewrite(func(node ast.Node) ast.Node {
		switch node := node.(type) {
		case *ast.CallExpr:
			id, ok := node.Fun.(*ast.Ident)
			if !ok {
				break
			}
			switch id.Name {
			case "systemstack":
				// TODO: Clean up func() { x }() -> { x }
				return &ast.CallExpr{Fun: node.Args[0], Args: []ast.Expr{}}
			case "mcall":
				return &ast.CallExpr{Fun: node.Args[0], Args: []ast.Expr{&ast.Ident{Name: "nil"}}}
			case "gopark":
				if cb, ok := node.Args[0].(*ast.Ident); ok && cb.Name == "nil" {
					break
				}
				return &ast.CallExpr{
					Fun: node.Args[0],
					Args: []ast.Expr{
						&ast.Ident{Name: "nil"},
						node.Args[1],
					},
				}
			case "goparkunlock":
				return &ast.CallExpr{
					Fun:  &ast.Ident{Name: "unlock"},
					Args: []ast.Expr{node.Args[0]},
				}
			}

		case *ast.FuncDecl:
			// TODO: Some functions are just too hairy for
			// the analysis right now.
			switch node.Name.Name {
			case "throw":
				node.Body = &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.ForStmt{
							Body: &ast.BlockStmt{},
						},
					},
				}
			case "traceEvent":
				node.Body = &ast.BlockStmt{}
			}
		}
		return node
	}, f)
}

var lockFn, unlockFn *ssa.Function

func registerLockQueries(pkg *ssa.Package, ptrConfig *pointer.Config) {
	lockFn = pkg.Members["lock"].(*ssa.Function)
	unlockFn = pkg.Members["unlock"].(*ssa.Function)
	for _, member := range pkg.Members {
		fn, ok := member.(*ssa.Function)
		if !ok {
			continue
		}
		for _, block := range fn.Blocks {
			for _, inst := range block.Instrs {
				call, ok := inst.(ssa.CallInstruction)
				if !ok {
					continue
				}
				target := call.Common().StaticCallee()
				if target == lockFn || target == unlockFn {
					ptrConfig.AddQuery(call.Common().Args[0])
				}
			}
		}
	}
}

// StringSpace interns strings into small integers.
type StringSpace struct {
	m map[string]int
	s []string
}

// NewStringSpace returns a new, empty StringSpace.
func NewStringSpace() *StringSpace {
	return &StringSpace{m: make(map[string]int)}
}

// Intern turns str into a small integer where Intern(x) == Intern(y)
// iff x == y.
func (sp *StringSpace) Intern(str string) int {
	if id, ok := sp.m[str]; ok {
		return id
	}
	id := len(sp.s)
	sp.s = append(sp.s, str)
	sp.m[str] = id
	return id
}

// LockSet represents a set of locks and where they were acquired.
type LockSet struct {
	sp     *StringSpace
	bits   big.Int
	stacks map[int]*StackFrame
}

type LockSetKey string

func NewLockSet(sp *StringSpace) *LockSet {
	return &LockSet{sp: sp}
}

func (set *LockSet) clone() *LockSet {
	out := &LockSet{sp: set.sp, stacks: map[int]*StackFrame{}}
	out.bits.Set(&set.bits)
	for k, v := range set.stacks {
		out.stacks[k] = v
	}
	return out
}

// Key returns a string such that two LockSet's Keys are == iff both
// LockSets have the same locks acquired at the same stacks.
func (set *LockSet) Key() LockSetKey {
	// TODO: This is complex enough now that maybe I just want a
	// hash function and an equality function.
	k := set.bits.Text(16)
	for i := 0; i < set.bits.BitLen(); i++ {
		if set.bits.Bit(i) != 0 {
			k += ":"
			for sf := set.stacks[i]; sf != nil; sf = sf.parent {
				k += fmt.Sprintf("%v,", sf.call.Pos())
			}
		}
	}
	return LockSetKey(k)
}

// Plus returns a LockSet that extends set with all locks in s,
// acquired at stack. If a lock in s is already in set, it does not
// get re-added. If all locks in s are in set, it returns set.
func (set *LockSet) Plus(s pointer.PointsToSet, stack *StackFrame) *LockSet {
	// TODO: Using the label strings is a hack. Internally, the
	// pointer package already represents PointsToSet as a sparse
	// integer set, but that isn't exposed. :(
	out := set
	for _, label := range s.Labels() {
		id := out.sp.Intern(label.String())
		if out.bits.Bit(id) != 0 {
			continue
		}
		if out == set {
			out = out.clone()
		}
		out.bits.SetBit(&out.bits, id, 1)
		out.stacks[id] = stack
	}
	return out
}

// Union returns a LockSet that is the union of set and o. If both set
// and o contain the same lock, the stack from set is preferred.
func (set *LockSet) Union(o *LockSet) *LockSet {
	var new big.Int
	new.AndNot(&o.bits, &set.bits)
	if new.Sign() == 0 {
		// Nothing to add.
		return set
	}

	out := set.clone()
	out.bits.Or(&out.bits, &o.bits)
	for k, v := range o.stacks {
		if out.stacks[k] == nil {
			out.stacks[k] = v
		}
	}
	return out
}

// Minus returns a LockSet that is like set, but does not contain any
// of the locks in s.
func (set *LockSet) Minus(s pointer.PointsToSet) *LockSet {
	out := set
	for _, label := range s.Labels() {
		id := out.sp.Intern(label.String())
		if out.bits.Bit(id) == 0 {
			continue
		}
		if out == set {
			out = out.clone()
		}
		out.bits.SetBit(&out.bits, id, 0)
		delete(out.stacks, id)
	}
	return out
}

// MinusLabel is like Minus, but for a specific string lock label.
func (set *LockSet) MinusLabel(label string) *LockSet {
	id := set.sp.Intern(label)
	if set.bits.Bit(id) == 0 {
		return set
	}
	out := set.clone()
	out.bits.SetBit(&out.bits, id, 0)
	delete(out.stacks, id)
	return out
}

func (set *LockSet) String() string {
	b := []byte("{")
	first := true
	for i := 0; i < set.bits.BitLen(); i++ {
		if set.bits.Bit(i) != 0 {
			if !first {
				b = append(b, ',')
			}
			first = false
			b = append(b, set.sp.s[i]...)
		}
	}
	return string(append(b, '}'))
}

// A LockSetSet is a set of LockSets.
type LockSetSet struct {
	M map[LockSetKey]*LockSet
}

func NewLockSetSet() *LockSetSet {
	return &LockSetSet{make(map[LockSetKey]*LockSet)}
}

func (lss *LockSetSet) Add(ss *LockSet) {
	lss.M[ss.Key()] = ss
}

func (lss *LockSetSet) Union(lss2 *LockSetSet) {
	if lss2 == nil {
		return
	}
	for k, ss := range lss2.M {
		lss.M[k] = ss
	}
}

func (lss *LockSetSet) ToSlice() []*LockSet {
	// TODO: Make deterministic?
	slice := make([]*LockSet, 0, len(lss.M))
	for _, ss := range lss.M {
		slice = append(slice, ss)
	}
	return slice
}

func (lss *LockSetSet) String() string {
	b := []byte("{")
	first := true
	for _, ss := range lss.M {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, ss.String()...)
	}
	return string(append(b, '}'))
}

// funcInfo contains analysis state for a single function.
type funcInfo struct {
	// exitLockSets maps from entry lock set to set of exit lock
	// sets. It memoizes the result of walkFunction.
	exitLockSets map[LockSetKey]*LockSetSet

	// ifDeps records the set of control-flow dependencies for
	// each ssa.BasicBlock of this function. These are the values
	// at entry to each block that may affect future control flow
	// decisions.
	ifDeps []map[ssa.Instruction]struct{}

	// debugTree is the block trace debug tree for this function.
	// If nil, this function is not being debug traced.
	debugTree *DebugTree
}

// StackFrame is a stack of call sites. A nil *StackFrame represents
// an empty stack.
type StackFrame struct {
	parent *StackFrame
	call   *ssa.Call
}

var internedStackFrames = make(map[StackFrame]*StackFrame)

// Flatten turns sf into a list of calls where the outer-most call is
// first.
func (sf *StackFrame) Flatten(into []*ssa.Call) []*ssa.Call {
	if sf == nil {
		if into == nil {
			return nil
		}
		return into[:0]
	}
	return append(sf.parent.Flatten(into), sf.call)
}

// Extend returns a new StackFrame that extends sf with call.
func (sf *StackFrame) Extend(call *ssa.Call) *StackFrame {
	return &StackFrame{sf, call}
}

// Intern returns a canonical *StackFrame such that a.Intern() ==
// b.Intern() iff a and b have the same sequence of calls.
func (sf *StackFrame) Intern() *StackFrame {
	if sf == nil {
		return nil
	}
	if sf, ok := internedStackFrames[*sf]; ok {
		return sf
	}
	nsf := sf.parent.Intern().Extend(sf.call)
	if nsf, ok := internedStackFrames[*nsf]; ok {
		return nsf
	}
	internedStackFrames[*nsf] = nsf
	return nsf
}

// TrimCommonPrefix eliminates the outermost frames that sf and other
// have in common and returns their distinct suffixes.
func (sf *StackFrame) TrimCommonPrefix(other *StackFrame) (*StackFrame, *StackFrame) {
	var buf [64]*ssa.Call
	f1 := sf.Flatten(buf[:])
	f2 := other.Flatten(f1[len(f1):cap(f1)])

	// Find the common prefix.
	var common int
	for common < len(f1) && common < len(f2) && f1[common] == f2[common] {
		common++
	}

	// Reconstitute.
	if common == 0 {
		return sf, other
	}
	var nsf1, nsf2 *StackFrame
	for _, call := range f1[common:] {
		nsf1 = nsf1.Extend(call)
	}
	for _, call := range f2[common:] {
		nsf2 = nsf2.Extend(call)
	}
	return nsf1, nsf2
}

type state struct {
	fset  *token.FileSet
	cg    *callgraph.Graph
	pta   *pointer.Result
	fns   map[*ssa.Function]*funcInfo
	stack *StackFrame

	lockOrder *LockOrder

	// roots is the list of root functions to visit.
	roots   []*ssa.Function
	rootSet map[*ssa.Function]struct{}

	// debugTree, if non-nil is the function CFG debug tree.
	debugTree *DebugTree
	// debugging indicates that we're debugging this subgraph of
	// the CFG.
	debugging bool
}

func (s *state) warnl(pos token.Pos, format string, args ...interface{}) {
	if pos.IsValid() {
		fmt.Printf("%s: ", s.fset.Position(pos))
	}
	fmt.Printf(format+"\n", args...)
}

// addRoot adds fn as a root of the control flow graph to visit.
func (s *state) addRoot(fn *ssa.Function) {
	if _, ok := s.rootSet[fn]; ok {
		return
	}
	s.roots = append(s.roots, fn)
	s.rootSet[fn] = struct{}{}
}

// callees returns the set of functions that call could possibly
// invoke. It returns nil for built-in functions or if pointer
// analysis failed.
func (s *state) callees(call ssa.CallInstruction) []*ssa.Function {
	if fn := call.Common().StaticCallee(); fn != nil {
		return []*ssa.Function{fn}
	} else if cnode := s.cg.Nodes[call.Parent()]; cnode != nil {
		var callees []*ssa.Function
		// TODO: Build an index in walkFunction?
		for _, o := range cnode.Out {
			if o.Site != call {
				continue
			}
			callees = append(callees, o.Callee.Func)
		}
		return callees
	}
	if _, ok := call.Common().Value.(*ssa.Builtin); ok {
		// Ignore these.
		//
		// TODO: Some of these we should turn into calls to
		// real runtime functions.
		return nil
	}

	s.warnl(call.Pos(), "no call graph for %v", call)
	return nil

}

// walkFunction explores f, given locks held on entry to f. It returns
// the set of locksets that can be held on exit from f.
//
// This implements the lockset algorithm from Engler and Ashcroft,
// SOSP 2003, plus simple path sensitivity to reduce mistakes from
// correlated control flow.
//
// TODO: A lot of call trees simply don't take locks. We could record
// that fact and fast-path the entry locks to the exit locks.
func (s *state) walkFunction(f *ssa.Function, locks *LockSet) *LockSetSet {
	fInfo := s.fns[f]
	if fInfo == nil {
		// First visit of this function.

		// Compute control-flow dependencies.
		//
		// TODO: Figure out which control flow decisions
		// actually affect locking and only track those. Right
		// now we hit a lot of simple increment loops that
		// cause path aborts, but don't involve any locking.
		// Find all of the branches that could lead to a
		// lock/unlock (the may-precede set) and eliminate
		// those where both directions will always lead to the
		// lock/unlock anyway (where the lock/unlock is in the
		// must-succeed set). This can be answered with the
		// post-dominator tree. This is basically the same
		// computation we need to propagate liveness over
		// control flow.
		var ifInstrs []ssa.Instruction
		for _, b := range f.Blocks {
			if len(b.Instrs) == 0 {
				continue
			}
			instr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
			if !ok {
				continue
			}
			ifInstrs = append(ifInstrs, instr)
		}
		ifDeps := livenessFor(f, ifInstrs)
		if debugFunctions[f.String()] {
			f.WriteTo(os.Stderr)
			fmt.Fprintf(os.Stderr, "if deps:\n")
			for bid, vals := range ifDeps {
				fmt.Fprintf(os.Stderr, "  %d: ", bid)
				for dep := range vals {
					fmt.Fprintf(os.Stderr, " %s", dep.(ssa.Value).Name())
				}
				fmt.Fprintf(os.Stderr, "\n")
			}
		}

		fInfo = &funcInfo{
			exitLockSets: make(map[LockSetKey]*LockSetSet),
			ifDeps:       ifDeps,
		}
		s.fns[f] = fInfo

		if f.Blocks == nil {
			s.warnl(f.Pos(), "external function %s", f)
		}

		if debugFunctions[f.String()] {
			fInfo.debugTree = new(DebugTree)
		}
	}

	if f.Blocks == nil {
		// External function. Assume it doesn't affect locks.
		lss1 := NewLockSetSet()
		lss1.Add(locks)
		return lss1
	}

	if debugFunctions[f.String()] && s.debugging == false {
		// Turn on debugging of this subtree.
		if s.debugTree == nil {
			s.debugTree = new(DebugTree)
		}
		s.debugging = true
		defer func() { s.debugging = false }()
	}

	if s.debugging {
		s.debugTree.Pushf("%s\nenter: %v", f, locks)
		defer s.debugTree.Pop()
	}

	// Check memoization cache.
	//
	// TODO: Our lockset can differ from a cached lockset by only
	// the stacks of the locks. Can we do something smarter than
	// recomputing the entire sub-graph in that situation? It's
	// rather complex because we may alter the lock order graph
	// with new stacks in the process. One could imagine tracking
	// a "predicate" and a compressed "delta" for the computation
	// and caching that.
	locksKey := locks.Key()
	if memo, ok := fInfo.exitLockSets[locksKey]; ok {
		if s.debugging {
			s.debugTree.Appendf("\ncached: %v", memo)
		}
		return memo
	}

	if fInfo.debugTree != nil {
		fInfo.debugTree.Pushf("enter lockset %v", locks)
		defer fInfo.debugTree.Pop()
	}

	// Resolve function cycles by returning an empty set of
	// locksets.
	//
	// TODO: RacerX detects cycles *without* regard to the entry
	// lock set. We could do that, but it doesn't seem to be an
	// issue to include the lock set. However, since we have the
	// lock set, maybe if we have a cycle with a non-empty lock
	// set we should report a self-deadlock.
	fInfo.exitLockSets[locksKey] = nil

	blockCache := make(blockCache)
	exitLockSets := NewLockSetSet()
	s.walkBlock(f.Blocks[0], blockCache, nil, locks, exitLockSets)
	fInfo.exitLockSets[locksKey] = exitLockSets
	//log.Printf("%s: %s -> %s", f.Name(), locks, exitLockSets)
	if s.debugging {
		s.debugTree.Appendf("\nexit: %v", exitLockSets)
	}
	return exitLockSets
}

// A blockCache is the set of visited states within a function. If
// walkBlock returns to the same block with the same state, it can
// terminate that path of execution.
type blockCache map[blockCacheKey][]blockCacheKey2

// blockCacheKey is the hashable part of the block cache key. This is
// used to quickly narrow down to a small set of blockCacheKey2 values
// that must be directly compared for equality.
type blockCacheKey struct {
	block   *ssa.BasicBlock
	lockset LockSetKey
}

// blockCacheKey2 is the un-hashable part of the block cache key.
type blockCacheKey2 struct {
	vs *ValState
}

// walkBlock visits b and all blocks reachable from b. The value state
// and lock set upon entry to b are vs and enterLockSet, respectively.
// When walkBlock reaches the return point of the function, it adds
// the possible lock sets at that point to exitLockSets.
func (s *state) walkBlock(b *ssa.BasicBlock, blockCache blockCache, vs *ValState, enterLockSet *LockSet, exitLockSets *LockSetSet) {
	f := b.Parent()
	debugTree := s.fns[f].debugTree
	if debugTree != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "block %v\nlockset %v\nvalue state:\n", b.Index, enterLockSet)
		vs.WriteTo(&buf)
		debugTree.Push(buf.String())
		defer debugTree.Pop()
	}

	bck := blockCacheKey{b, enterLockSet.Key()}
	if bck2s, ok := blockCache[bck]; ok {
		for _, bck2 := range bck2s {
			// Check the values that are live at this
			// block. Note that the live set includes phis
			// at the beginning of this block if they
			// participate in control flow decisions, so
			// we'll pick up any phi values assigned by
			// our called.
			if bck2.vs.EqualAt(vs, s.fns[f].ifDeps[b.Index]) {
				// Terminate recursion. Some other
				// path has already visited here with
				// this lock set and value state.
				if debugTree != nil {
					debugTree.Leaf("cached")
				}
				return
			}
		}
		if len(bck2s) > 10 {
			s.warnl(blockPos(b), "too many states, trimming path (block %d)", b.Index)
			// for _, bck2 := range bck2s {
			// 	log.Print("next ", f.Name(), ":", b.Index)
			// 	bck2.vs.WriteTo(os.Stderr)
			// }
			if debugTree != nil {
				debugTree.Leaf("too many states")
			}
			return
		}
	}
	blockCache[bck] = append(blockCache[bck], blockCacheKey2{vs})

	// For each instruction, compute the effect of that
	// instruction on all possible lock sets at that point.
	lockSets := NewLockSetSet()
	lockSets.Add(enterLockSet)
	var ifCond ssa.Value
	for _, instr := range b.Instrs {
		// Update value state with the effect of this
		// instruction.
		vs = vs.Do(instr)

		switch instr := instr.(type) {
		case *ssa.If:
			// We'll bind ifCond to true or false when we
			// visit successors.
			ifCond = instr.Cond

		case *ssa.Call:
			// TODO: There are other types of
			// ssa.CallInstructions, but they have different
			// control flow.
			outs := s.callees(instr)
			if len(outs) == 0 {
				// This is a built-in like print or
				// len. Assume it doesn't affect the
				// locksets.
				break
			}
			nextLockSets := NewLockSetSet()
			s.stack = s.stack.Extend(instr)
			for _, o := range outs {
				// TODO: _Gscan locks, misc locks, semaphores
				if o == lockFn {
					lock := s.pta.Queries[instr.Call.Args[0]].PointsTo()
					for _, ls := range lockSets.M {
						s.lockOrder.Add(ls, lock, s.stack)
						ls2 := ls.Plus(lock, s.stack)
						// If we
						// self-deadlocked,
						// terminate this
						// path.
						if ls != ls2 {
							nextLockSets.Add(ls2)
						}
					}
				} else if o == unlockFn {
					lock := s.pta.Queries[instr.Call.Args[0]].PointsTo()
					for _, ls := range lockSets.M {
						// TODO: Warn on
						// unlock of unlocked
						// lock.
						ls = ls.Minus(lock)
						nextLockSets.Add(ls)
					}
				} else {
					for _, ls := range lockSets.M {
						nextLockSets.Union(s.walkFunction(o, ls))
					}
				}
			}
			s.stack = s.stack.parent
			lockSets = nextLockSets

		case *ssa.Go:
			for _, o := range s.callees(instr) {
				//log.Printf("found go %s; adding to roots", o)
				s.addRoot(o)
			}

		case *ssa.Return:
			// We've reached function exit. Add the
			// current lock set to exitLockSets.
			//
			// TODO: Handle defers.

			// Special case: we can't handle
			// inter-procedural correlated control flow
			// between traceAcquireBuffer and
			// traceReleaseBuffer, so hard-code that
			// traceReleaseBuffer releases
			// runtime.trace.bufLock.
			if f.Name() == "traceReleaseBuffer" {
				nextLockSets := NewLockSetSet()
				for _, ls := range lockSets.M {
					ls = ls.MinusLabel("runtime.trace.bufLock")
					nextLockSets.Add(ls)
				}
				lockSets = nextLockSets
			}

			exitLockSets.Union(lockSets)
		}
	}

	// Annoyingly, the last instruction in an ssa.BasicBlock
	// doesn't have a location, even if it obviously corresponds
	// to a source statement. exitPos guesses one.
	exitPos := func(b *ssa.BasicBlock) token.Pos {
		for b != nil {
			for i := len(b.Instrs) - 1; i >= 0; i-- {
				if pos := b.Instrs[i].Pos(); pos != 0 {
					return pos
				}
			}
			if len(b.Preds) == 0 {
				break
			}
			b = b.Preds[0]
		}
		return 0
	}
	_ = exitPos

	// If this is an "if", see if we have enough information to
	// determine its direction.
	succs := b.Succs
	if ifCond != nil {
		x := vs.Get(ifCond)
		if x != nil {
			//log.Printf("determined control flow at %s: %v", s.fset.Position(exitPos(b)), x)
			if constant.BoolVal(x.(DynConst).c) {
				// Take true path.
				succs = succs[:1]
			} else {
				// Take false path.
				succs = succs[1:]
			}
		}
	}
	if len(lockSets.M) == 0 && debugTree != nil {
		// This happens after functions that don't return.
		debugTree.Leaf("no locksets")
	}

	// Process block successors.
	for _, succLockSet := range lockSets.M {
		for i, b2 := range succs {
			vs2 := vs
			if ifCond != nil {
				// TODO: We could back-propagate this
				// in simple cases, like when ifCond
				// is a == BinOp. (And we could
				// forward-propagate that! Hmm.)
				vs2 = vs2.Extend(ifCond, DynConst{constant.MakeBool(i == 0)})
			}

			// Propagate values over phis at the beginning
			// of b2.
			for _, instr := range b2.Instrs {
				instr, ok := instr.(*ssa.Phi)
				if !ok {
					break
				}
				for i, inval := range instr.Edges {
					if b2.Preds[i] == b {
						x := vs2.Get(inval)
						if x != nil {
							vs2 = vs2.Extend(instr, x)
						}
					}
				}
			}

			if debugTree != nil && len(b.Succs) > 1 {
				if b2 == b.Succs[0] {
					debugTree.SetEdge("T")
				} else if b2 == b.Succs[1] {
					debugTree.SetEdge("F")
				}
			}
			s.walkBlock(b2, blockCache, vs2, succLockSet, exitLockSets)
		}
	}
}

// blockPos returns the best position it can for b.
func blockPos(b *ssa.BasicBlock) token.Pos {
	var visited []bool
	for {
		if visited != nil {
			if visited[b.Index] {
				// Give up.
				return b.Parent().Pos()
			}
			visited[b.Index] = true
		}
		// Phis have useless line numbers. Find the first
		// "real" instruction.
		for _, i := range b.Instrs {
			if _, ok := i.(*ssa.Phi); ok || !i.Pos().IsValid() {
				continue
			}
			return i.Pos()
		}
		if len(b.Preds) == 0 {
			return b.Parent().Pos()
		}
		// Try b's predecessor.
		if visited == nil {
			// Delayed allocation of visited.
			visited = make([]bool, len(b.Parent().Blocks))
			visited[b.Index] = true
		}
		b = b.Preds[0]
	}
}
