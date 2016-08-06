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
		debugFuncs   string
	)
	flag.StringVar(&outLockGraph, "lockgraph", "", "write lock graph in dot to `file`")
	flag.StringVar(&outCallGraph, "callgraph", "", "write call graph in dot to `file`")
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
			fmt.Fprintln(w, "digraph callgraph {")
			callgraph.GraphVisitEdges(pta.CallGraph, func(e *callgraph.Edge) error {
				fmt.Fprintf(w, "%q -> %q;\n", e.Caller, e.Callee)
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
		// TODO: Warn if any locks are held at return.
		root := s.roots[i]
		exitLockSets := s.walkFunction(root, NewLockSet(stringSpace))
		log.Print("locks at return: ", exitLockSets)
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

	// Output lock graph if requested.
	if outLockGraph != "" {
		withWriter(outLockGraph, s.lockOrder.WriteToDot)
	}

	// Output text lock cycle report.
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
				node.Body = nil
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
					log.Print("found ", inst, " in ", member)
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
	// TODO: This happens for print and println, which we should
	// turn into calls to their implementation functions.
	log.Print("no call graph for ", call, " in ", call.Parent(), " at ", s.fset.Position(call.Pos()))
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
			log.Println("warning: external function", f.Name())
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
	log.Printf("%s: %s -> %s", f.Name(), locks, exitLockSets)
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
			log.Print("warning: too many states at ", f.Name(), " block ", b.Index, "; giving up on path")
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
				log.Printf("found go %s; adding to roots", o)
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

	// If this is an "if", see if we have enough information to
	// determine its direction.
	succs := b.Succs
	if ifCond != nil {
		x := vs.Get(ifCond)
		if x != nil {
			log.Printf("determined control flow at %s: %v", s.fset.Position(exitPos(b)), x)
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
