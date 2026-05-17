package analyzer

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

type LoadMode int

const (
	LoadModeSyntax LoadMode = iota
	LoadModeTypes
	LoadModeSSA
)

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

	// Mode indicates how much of the Go program was loaded.
	// It is used to serve lightweight queries without allocating gigabytes of memory.
	Mode LoadMode

	// importLocs caches per-package import source locations extracted during
	// load (before pkg.Syntax is cleared). Key: pkg.PkgPath → importPath → loc.
	// Eliminates the need to re-parse source files in CheckArchitectureBoundaries.
	importLocs map[string]map[string]importSourceLoc

	// httpRoutes caches all HTTP route registrations extracted during load
	// (before pkg.Syntax and pkg.CompiledGoFiles are trimmed). Eliminates
	// full source re-parsing on every ListHTTPRoutes call.
	httpRoutes []HTTPRoute

	// grpcEndpoints and grpcRegistrations cache generated grpc-go service
	// descriptors and registration call sites extracted during load.
	grpcEndpoints     []GRPCEndpoint
	grpcRegistrations []GRPCRegistration

	// methodFingerprints caches per-function/method body hashes captured
	// during load (before pkg.Syntax is cleared). Used by
	// FindDuplicateMethods to detect copy-pasted implementations without
	// re-parsing source.
	methodFingerprints []MethodFingerprint

	// complexityMetrics caches per-function complexity scores captured during
	// load (before pkg.Syntax is cleared). Used by ComputeComplexityMetrics
	// without re-parsing source files.
	complexityMetrics []FunctionComplexity

	// concurrencySummaries caches per-function access summaries captured lazily
	// for DetectConcurrencyRisks and related analyses.
	concurrencyOnce      sync.Once
	concurrencySummaries map[*ssa.Function]FunctionAccessSummary

	// gormModels caches structs with GORM tags captured during load
	// (before pkg.Syntax is cleared). Used by FindOrphanedDatabaseModels
	// without re-parsing source files.
	ormModels []OrmModel

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

func (p *LoadedProgram) ConcurrencySummaries() map[*ssa.Function]FunctionAccessSummary {
	p.concurrencyOnce.Do(func() {
		p.concurrencySummaries = BuildFunctionAccessSummaries(p)
	})
	return p.concurrencySummaries
}

type cacheKey string

type cacheEntry struct {
	key        cacheKey
	rootPath   string
	patterns   []string
	prog       *LoadedProgram
	lastAccess time.Time
}

type CacheRecord struct {
	Key             string   `json:"key"`
	RootPath        string   `json:"root_path"`
	PackagePatterns []string `json:"package_patterns"`
	PackagesLoaded  int      `json:"packages_loaded"`
	FunctionsLoaded int      `json:"functions_loaded"`
	LastAccess      string   `json:"last_access,omitempty"`
}

// OrmModel represents a database model discovered during load.
type OrmModel struct {
	Name      string
	Pkg       string
	File      string
	Line      int
	Framework string // "gorm", "ent", "sqlx", etc.
}

// Workspace is a process-scoped LRU cache of LoadedProgram instances guarded
// by a mutex. Concurrent loads of the same key are coalesced via singleflight.
type Workspace struct {
	mu        sync.Mutex
	capacity  int
	ttl       time.Duration
	cache     map[cacheKey]*list.Element
	order     *list.List // most-recently-used at the front
	group     singleflight.Group
	done      chan struct{}
	closeOnce sync.Once
}

func NewWorkspace() *Workspace {
	capacity := defaultCacheCapacity
	if raw := strings.TrimSpace(os.Getenv("GO_ARCH_XRAY_CACHE_CAPACITY")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
			capacity = n
		}
	}
	w := &Workspace{
		capacity: capacity,
		ttl:      15 * time.Minute, // Default TTL
		cache:    make(map[cacheKey]*list.Element),
		order:    list.New(),
		done:     make(chan struct{}),
	}
	go w.sweeper()
	return w
}

// Close stops the background cache sweeper. It is mainly useful for tests and
// short-lived embedded uses; server processes can keep the workspace open for
// their lifetime.
func (w *Workspace) Close() {
	w.closeOnce.Do(func() {
		close(w.done)
	})
}

// SetTTL configures the time-to-live for idle cache entries. Set to 0 to disable.
func (w *Workspace) SetTTL(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ttl = d
}

func (w *Workspace) sweeper() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.sweep()
		case <-w.done:
			return
		}
	}
}

func (w *Workspace) sweep() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.ttl <= 0 {
		return
	}

	now := time.Now()
	// Iterate from the back (least recently used)
	for w.order.Len() > 0 {
		elem := w.order.Back()
		entry := elem.Value.(*cacheEntry)
		if now.Sub(entry.lastAccess) > w.ttl {
			delete(w.cache, entry.key)
			w.order.Remove(elem)
			logger.Printf("evicted idle cached program %s (inactive for %v)", entry.key, now.Sub(entry.lastAccess))
		} else {
			// Since order is sorted by MRU, if the back isn't expired, nothing before it is
			break
		}
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

// Status returns cache size/capacity plus LRU-ordered metadata for entries.
func (w *Workspace) Status() (size int, capacity int, entries []CacheRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries = make([]CacheRecord, 0, w.order.Len())
	for elem := w.order.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		rec := CacheRecord{
			Key:             string(entry.key),
			RootPath:        entry.rootPath,
			PackagePatterns: append([]string(nil), entry.patterns...),
			PackagesLoaded:  len(entry.prog.Packages),
			FunctionsLoaded: len(entry.prog.SSAFuncs),
			LastAccess:      entry.lastAccess.Format(time.RFC3339),
		}
		entries = append(entries, rec)
	}

	return w.order.Len(), w.capacity, entries
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
	return w.getOrLoad(dir, pattern, LoadModeTypes)
}

func (w *Workspace) GetOrLoadSyntaxOnly(dir, pattern string) (*LoadedProgram, error) {
	return w.getOrLoad(dir, pattern, LoadModeSyntax)
}

func (w *Workspace) GetOrLoadSSA(dir, pattern string) (*LoadedProgram, error) {
	return w.getOrLoad(dir, pattern, LoadModeSSA)
}

func (w *Workspace) getOrLoad(dir, pattern string, mode LoadMode) (*LoadedProgram, error) {
	patterns := normalizePatternsForDir(dir, SplitPatterns(pattern))
	key := makeCacheKey(dir, patterns)

	w.mu.Lock()
	if elem, ok := w.cache[key]; ok {
		prog := elem.Value.(*cacheEntry).prog
		if prog.Mode >= mode {
			elem.Value.(*cacheEntry).lastAccess = time.Now()
			w.order.MoveToFront(elem)
			w.mu.Unlock()
			return prog, nil
		}
	}
	w.mu.Unlock()

	v, err, _ := w.group.Do(fmt.Sprintf("%s:%d", key, mode), func() (any, error) {
		prog, err := loadProgram(dir, patterns, mode)
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		now := time.Now()
		if elem, ok := w.cache[key]; ok {
			if elem.Value.(*cacheEntry).prog.Mode >= mode {
				elem.Value.(*cacheEntry).lastAccess = now
				w.order.MoveToFront(elem)
				return elem.Value.(*cacheEntry).prog, nil
			}
			// Replace existing with upgraded program
			elem.Value.(*cacheEntry).prog = prog
			elem.Value.(*cacheEntry).lastAccess = now
			w.order.MoveToFront(elem)
			return prog, nil
		}
		elem := w.order.PushFront(&cacheEntry{key: key, rootPath: dir, patterns: append([]string(nil), patterns...), prog: prog, lastAccess: now})
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
	key := makeCacheKey(dir, normalizePatternsForDir(dir, SplitPatterns(pattern)))
	w.mu.Lock()
	if elem, ok := w.cache[key]; ok {
		delete(w.cache, key)
		w.order.Remove(elem)
	}
	w.mu.Unlock()
}

// Clear removes a single cached program by key inputs. Returns true when
// an entry existed and was removed.
func (w *Workspace) Clear(dir, pattern string) bool {
	key := makeCacheKey(dir, normalizePatternsForDir(dir, SplitPatterns(pattern)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if elem, ok := w.cache[key]; ok {
		delete(w.cache, key)
		w.order.Remove(elem)
		return true
	}
	return false
}

// ClearAll removes all cached programs and returns the number of entries
// removed.
func (w *Workspace) ClearAll() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	removed := w.order.Len()
	w.cache = make(map[cacheKey]*list.Element)
	w.order = list.New()
	return removed
}

func (w *Workspace) Reload(dir, pattern string) (*LoadedProgram, error) {
	w.Invalidate(dir, pattern)
	return w.GetOrLoad(dir, pattern)
}

func loadProgram(dir string, patterns []string, mode LoadMode) (*LoadedProgram, error) {
	patterns = normalizePatternsForDir(dir, patterns)

	var pkgs []*packages.Package
	var err error

	if mode == LoadModeSyntax {
		pkgs, err = loadSyntaxOnlyFast(dir, patterns)
	} else {
		pkgMode := packages.NeedName | packages.NeedCompiledGoFiles | packages.NeedSyntax
		if mode >= LoadModeTypes {
			pkgMode |= packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedModule | packages.NeedImports
		}

		cfg := &packages.Config{
			Mode:  pkgMode,
			Dir:   dir,
			Tests: false,
		}

		pkgs, err = packages.Load(cfg, patterns...)
	}

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
		if mode >= LoadModeTypes {
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
	}

	var prog *ssa.Program
	var funcs []*ssa.Function
	var importLocsCache map[string]map[string]importSourceLoc

	rootPaths := make(map[string]bool, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.PkgPath != "" {
			rootPaths[pkg.PkgPath] = true
		}
	}

	if mode == LoadModeSSA {
		// Build SSA bodies only for the requested (root) packages. Transitive
		// dependencies are still represented in the SSA program as type-only
		// entries so we can resolve cross-package types, but we never pay the
		// memory cost of compiling stdlib bodies into SSA. ssa.BareInits skips
		// init function synthesis to further trim memory.
		prog, _ = ssautil.Packages(pkgs, ssa.InstantiateGenerics|ssa.BareInits)
		if prog == nil {
			return nil, fmt.Errorf("ssa program could not be created (likely due to type errors above)")
		}
		prog.Build()

		// Because we used ssautil.Packages (root-only build), AllFunctions
		// already returns ~root SSA funcs only. Filter defensively to root
		// packages so analyzers never traverse synthetic stdlib wrappers.
		allFuncs := ssautil.AllFunctions(prog)
		funcs = make([]*ssa.Function, 0, len(allFuncs))
		for fn := range allFuncs {
			if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
				continue
			}
			if !rootPaths[fn.Pkg.Pkg.Path()] {
				continue
			}
			funcs = append(funcs, fn)
		}
	}

	if mode >= LoadModeTypes {
		importLocsCache = make(map[string]map[string]importSourceLoc, len(pkgs))
		for _, pkg := range pkgs {
			importLocsCache[pkg.PkgPath] = extractImportLocsFromPkg(pkg)
		}
	}

	// Capture import source locations and HTTP routes from syntax BEFORE
	// clearing pkg.Syntax and trimming pkg.CompiledGoFiles. This eliminates
	// the need to re-parse source files on every analysis call.
	var httpRoutesCache []HTTPRoute
	var grpcCache grpcExtraction
	var methodFingerprintsCache []MethodFingerprint
	var complexityMetricsCache []FunctionComplexity
	var ormModelsCache []OrmModel

	var wg sync.WaitGroup
	wg.Add(5)

	go func() {
		defer wg.Done()
		httpRoutesCache = extractRoutesFromSyntax(pkgs)
	}()
	go func() {
		defer wg.Done()
		grpcCache = extractGRPCFromSyntax(pkgs)
	}()
	go func() {
		defer wg.Done()
		methodFingerprintsCache = extractMethodFingerprintsFromSyntax(pkgs)
	}()
	go func() {
		defer wg.Done()
		complexityMetricsCache = extractComplexityFromSyntax(pkgs)
	}()
	go func() {
		defer wg.Done()
		ormModelsCache = extractOrmModelsFromSyntax(pkgs)
	}()

	wg.Wait()

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

	logger.Printf("loaded %d packages, %d root functions from %s patterns=%v mode=%d", len(pkgs), len(funcs), dir, patterns, mode)

	return &LoadedProgram{
		Packages:             pkgs,
		SSA:                  prog,
		SSAFuncs:             funcs,
		RootPaths:            rootPaths,
		Patterns:             patterns,
		Mode:                 mode,
		importLocs:           importLocsCache,
		httpRoutes:           httpRoutesCache,
		grpcEndpoints:        grpcCache.endpoints,
		grpcRegistrations:    grpcCache.registrations,
		methodFingerprints:   methodFingerprintsCache,
		complexityMetrics:    complexityMetricsCache,
		ormModels:            ormModelsCache,
		concurrencySummaries: nil,
	}, nil
}

func normalizePatternsForDir(dir string, patterns []string) []string {
	seen := make(map[string]bool, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		norm := normalizePatternForDir(dir, pattern)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	if len(out) == 0 {
		return []string{"./..."}
	}
	return out
}

func normalizePatternForDir(dir, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}

	unixPattern := strings.ReplaceAll(pattern, "\\", "/")
	base := unixPattern
	recursive := false
	if strings.HasSuffix(unixPattern, "/...") {
		recursive = true
		base = strings.TrimSuffix(unixPattern, "/...")
	}

	absBase := filepath.FromSlash(base)
	if !filepath.IsAbs(absBase) {
		absBase = filepath.Join(dir, absBase)
	}
	if !pathExists(absBase) {
		return unixPattern
	}

	rel, err := filepath.Rel(dir, absBase)
	if err != nil {
		return unixPattern
	}

	rel = filepath.ToSlash(rel)
	if rel == "." {
		if recursive {
			return "./..."
		}
		return "."
	}

	norm := "./" + rel
	if recursive {
		return norm + "/..."
	}
	return norm
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// parseGoWorkModuleDirs reads the go.work file at goWorkPath and returns the
// relative patterns (./subdir/...) for each `use` directive, excluding "." which
// maps to "./...". The returned patterns can be passed directly to GetOrLoad.
func parseGoWorkModuleDirs(goWorkPath string) []string {
	data, err := os.ReadFile(goWorkPath)
	if err != nil {
		return nil
	}
	var patterns []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Match bare `use ./path` or entries inside a `use ( ... )` block
		useVal := ""
		if strings.HasPrefix(line, "use ") {
			useVal = strings.TrimSpace(strings.TrimPrefix(line, "use "))
			// strip inline comment
			if idx := strings.Index(useVal, "//"); idx >= 0 {
				useVal = strings.TrimSpace(useVal[:idx])
			}
			// "use (" opens a block — not a path
			if useVal == "(" {
				useVal = ""
			}
		} else if !strings.HasPrefix(line, "go ") && !strings.HasPrefix(line, "toolchain ") &&
			line != "(" && line != ")" && line != "" &&
			!strings.HasPrefix(line, "//") {
			// bare path inside a use ( ... ) block
			useVal = line
		}
		if useVal == "" || useVal == "." {
			continue
		}
		// Convert to ./subdir/... pattern
		rel := strings.TrimPrefix(strings.ReplaceAll(useVal, "\\", "/"), "./")
		patterns = append(patterns, "./"+rel+"/...")
	}
	return patterns
}

// WorkspaceFallbackPatterns returns additional package patterns to try when
// a function is not found under the given pattern. It checks for a go.work file
// at dir and returns ./submod/... patterns for each non-root workspace module.
func WorkspaceFallbackPatterns(dir string) []string {
	goWork := filepath.Join(dir, "go.work")
	if !pathExists(goWork) {
		return []string{"./..."}
	}
	extra := parseGoWorkModuleDirs(goWork)
	patterns := make([]string, 0, 1+len(extra))
	patterns = append(patterns, "./...")
	patterns = append(patterns, extra...)
	return patterns
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

func loadSyntaxOnlyFast(dir string, patterns []string) ([]*packages.Package, error) {
	fset := token.NewFileSet()
	pkgs := make(map[string]*packages.Package)

	// Attempt to find module path
	modPath := ""
	if modBytes, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		for _, line := range strings.Split(string(modBytes), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				modPath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}

	processDir := func(dpath string, recursive bool) error {
		return filepath.WalkDir(dpath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if path != dpath && !recursive {
					return filepath.SkipDir
				}
				name := d.Name()
				if name == "vendor" || name == ".git" || name == "testdata" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}

			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil || file == nil {
				return nil
			}

			fileDir := filepath.ToSlash(filepath.Dir(path))
			pkg, ok := pkgs[fileDir]
			if !ok {
				pkgPath := fileDir
				rel, rerr := filepath.Rel(dir, filepath.FromSlash(fileDir))
				if rerr == nil {
					rel = filepath.ToSlash(rel)
					if rel == "." {
						if modPath != "" {
							pkgPath = modPath
						}
					} else {
						if modPath != "" {
							pkgPath = modPath + "/" + rel
						} else {
							pkgPath = rel
						}
					}
				}

				pkg = &packages.Package{
					ID:      pkgPath,
					PkgPath: pkgPath,
					Name:    file.Name.Name,
					Fset:    fset,
				}
				pkgs[fileDir] = pkg
			}
			pkg.Syntax = append(pkg.Syntax, file)
			pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, path)
			return nil
		})
	}

	for _, p := range patterns {
		recursive := strings.HasSuffix(p, "/...") || p == "..."
		base := p
		if recursive {
			base = strings.TrimSuffix(p, "/...")
		}

		absBase := filepath.FromSlash(base)
		if !filepath.IsAbs(absBase) {
			absBase = filepath.Join(dir, absBase)
		}

		if err := processDir(absBase, recursive); err != nil {
			return nil, err
		}
	}

	result := make([]*packages.Package, 0, len(pkgs))
	for _, pkg := range pkgs {
		result = append(result, pkg)
	}
	return result, nil
}
