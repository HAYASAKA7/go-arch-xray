package analyzer

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// defaultCacheCapacity caps how many distinct (dir, patterns) programs the
// workspace keeps live at once. SSA programs are very memory-heavy, so we
// evict aggressively. Callers can tune via SetCapacity.
const defaultCacheCapacity = 2

var logger = log.New(os.Stderr, "[go-arch-xray] ", log.LstdFlags)

// LoadedProgram is a cached snapshot of a Go workspace analyzed via go/packages
// and golang.org/x/tools/go/ssa. SSA bodies are built only for the requested
// (root) packages; transitive dependencies are kept as type-only entries to
// keep memory bounded.
type LoadedProgram struct {
	Packages  []*packages.Package
	SSA       *ssa.Program
	SSAFuncs  []*ssa.Function
	RootPaths map[string]bool
	Patterns  []string

	chaOnce  sync.Once
	chaGraph *callgraph.Graph
}

// CallGraph builds a CHA call graph lazily and caches it on the program so
// repeated call-hierarchy queries don't rebuild this expensive structure.
func (p *LoadedProgram) CallGraph() *callgraph.Graph {
	p.chaOnce.Do(func() {
		p.chaGraph = cha.CallGraph(p.SSA)
	})
	return p.chaGraph
}

type cacheKey string

type cacheEntry struct {
	key  cacheKey
	prog *LoadedProgram
}

// Workspace is a process-scoped LRU cache of LoadedProgram instances guarded
// by a mutex. Concurrent loads of the same key are coalesced via singleflight.
type Workspace struct {
	mu       sync.Mutex
	capacity int
	cache    map[cacheKey]*list.Element
	order    *list.List // most-recently-used at the front
	group    singleflight.Group
}

func NewWorkspace() *Workspace {
	return &Workspace{
		capacity: defaultCacheCapacity,
		cache:    make(map[cacheKey]*list.Element),
		order:    list.New(),
	}
}

// SetCapacity changes the maximum number of cached programs. Must be >= 1.
func (w *Workspace) SetCapacity(n int) {
	if n < 1 {
		n = 1
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.capacity = n
	w.evictLocked()
}

// Stats returns the current number of cached programs and the configured cap.
func (w *Workspace) Stats() (size int, capacity int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.order.Len(), w.capacity
}

// SplitPatterns turns a comma- or whitespace-separated pattern string into a
// deduplicated, trimmed list of go/packages patterns. An empty input yields
// the default "./..." pattern so callers always receive at least one entry.
func SplitPatterns(pattern string) []string {
	if pattern == "" {
		return []string{"./..."}
	}
	rawParts := strings.FieldsFunc(pattern, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t'
	})
	seen := make(map[string]bool, len(rawParts))
	out := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		s := strings.TrimSpace(p)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{"./..."}
	}
	return out
}

func makeCacheKey(dir string, patterns []string) cacheKey {
	sorted := append([]string(nil), patterns...)
	sort.Strings(sorted)
	h := sha256.New()
	h.Write([]byte(dir))
	for _, p := range sorted {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return cacheKey(fmt.Sprintf("%x", sum[:8]))
}

func (w *Workspace) GetOrLoad(dir, pattern string) (*LoadedProgram, error) {
	patterns := SplitPatterns(pattern)
	key := makeCacheKey(dir, patterns)

	w.mu.Lock()
	if elem, ok := w.cache[key]; ok {
		w.order.MoveToFront(elem)
		prog := elem.Value.(*cacheEntry).prog
		w.mu.Unlock()
		return prog, nil
	}
	w.mu.Unlock()

	v, err, _ := w.group.Do(string(key), func() (any, error) {
		prog, err := loadProgram(dir, patterns)
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if elem, ok := w.cache[key]; ok {
			w.order.MoveToFront(elem)
			return elem.Value.(*cacheEntry).prog, nil
		}
		elem := w.order.PushFront(&cacheEntry{key: key, prog: prog})
		w.cache[key] = elem
		w.evictLocked()
		return prog, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*LoadedProgram), nil
}

func (w *Workspace) evictLocked() {
	for w.order.Len() > w.capacity {
		tail := w.order.Back()
		if tail == nil {
			return
		}
		entry := tail.Value.(*cacheEntry)
		delete(w.cache, entry.key)
		w.order.Remove(tail)
		logger.Printf("evicted cached program %s", entry.key)
	}
}

func (w *Workspace) Invalidate(dir, pattern string) {
	key := makeCacheKey(dir, SplitPatterns(pattern))
	w.mu.Lock()
	if elem, ok := w.cache[key]; ok {
		delete(w.cache, key)
		w.order.Remove(elem)
	}
	w.mu.Unlock()
}

func (w *Workspace) Reload(dir, pattern string) (*LoadedProgram, error) {
	w.Invalidate(dir, pattern)
	return w.GetOrLoad(dir, pattern)
}

func loadProgram(dir string, patterns []string) (*LoadedProgram, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedDeps |
			packages.NeedImports,
		Dir:   dir,
		Tests: false,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages matched patterns %v under %s", patterns, dir)
	}

	var loadErrs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrs = append(loadErrs, fmt.Errorf("%s: %s", pkg.PkgPath, e.Msg))
		}
	}
	if len(loadErrs) > 0 {
		for _, e := range loadErrs {
			logger.Printf("package error: %v", e)
		}
		hasTypes := false
		for _, pkg := range pkgs {
			if pkg.Types != nil {
				hasTypes = true
				break
			}
		}
		if !hasTypes {
			return nil, fmt.Errorf("all packages failed to load: %v", loadErrs[0])
		}
	}

	// Build SSA bodies only for the requested (root) packages. Transitive
	// dependencies are still represented in the SSA program as type-only
	// entries so we can resolve cross-package types, but we never pay the
	// memory cost of compiling stdlib bodies into SSA. ssa.BareInits skips
	// init function synthesis to further trim memory.
	prog, _ := ssautil.Packages(pkgs, ssa.InstantiateGenerics|ssa.BareInits)
	if prog == nil {
		return nil, fmt.Errorf("ssa program could not be created (likely due to type errors above)")
	}
	prog.Build()

	rootPaths := make(map[string]bool, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.PkgPath != "" {
			rootPaths[pkg.PkgPath] = true
		}
	}

	// Drop syntax / type info / file listings from every reachable package
	// to release the bulk of go/packages memory once SSA is built. The
	// downstream analyzers only need pkg.Types.Scope(), pkg.PkgPath,
	// pkg.Imports, pkg.CompiledGoFiles[0] and pkg.Fset, all of which survive.
	seen := make(map[*packages.Package]bool)
	var clear func(*packages.Package)
	clear = func(pkg *packages.Package) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		pkg.Syntax = nil
		pkg.TypesInfo = nil
		pkg.IllTyped = false
		pkg.GoFiles = nil
		pkg.OtherFiles = nil
		pkg.EmbedFiles = nil
		pkg.EmbedPatterns = nil
		pkg.IgnoredFiles = nil
		// Preserve only the first compiled go file for context anchors.
		if len(pkg.CompiledGoFiles) > 1 {
			pkg.CompiledGoFiles = pkg.CompiledGoFiles[:1:1]
		}
		for _, imp := range pkg.Imports {
			clear(imp)
		}
	}
	for _, pkg := range pkgs {
		clear(pkg)
	}

	// Because we used ssautil.Packages (root-only build), AllFunctions
	// already returns ~root SSA funcs only. Filter defensively to root
	// packages so analyzers never traverse synthetic stdlib wrappers.
	allFuncs := ssautil.AllFunctions(prog)
	funcs := make([]*ssa.Function, 0, len(allFuncs))
	for fn := range allFuncs {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
			continue
		}
		if !rootPaths[fn.Pkg.Pkg.Path()] {
			continue
		}
		funcs = append(funcs, fn)
	}

	logger.Printf("loaded %d packages, %d root functions from %s patterns=%v", len(pkgs), len(funcs), dir, patterns)

	return &LoadedProgram{
		Packages:  pkgs,
		SSA:       prog,
		SSAFuncs:  funcs,
		RootPaths: rootPaths,
		Patterns:  patterns,
	}, nil
}

// AllLoadedPackages returns the union of root packages and all transitively
// imported packages reachable from them, keyed by package path. Packages with
// empty paths are skipped.
func AllLoadedPackages(roots []*packages.Package) map[string]*packages.Package {
	out := make(map[string]*packages.Package)
	var walk func(*packages.Package)
	walk = func(pkg *packages.Package) {
		if pkg == nil || pkg.PkgPath == "" || out[pkg.PkgPath] != nil {
			return
		}
		out[pkg.PkgPath] = pkg
		for _, imp := range pkg.Imports {
			walk(imp)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return out
}
