package analyzer

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"sync"

	"golang.org/x/sync/singleflight"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

var logger = log.New(os.Stderr, "[go-arch-xray] ", log.LstdFlags)

type LoadedProgram struct {
	Packages []*packages.Package
	SSA      *ssa.Program
	SSAFuncs []*ssa.Function
}

type cacheKey string

type Workspace struct {
	mu    sync.RWMutex
	cache map[cacheKey]*LoadedProgram
	group singleflight.Group
}

func NewWorkspace() *Workspace {
	return &Workspace{
		cache: make(map[cacheKey]*LoadedProgram),
	}
}

func makeCacheKey(dir, pattern string) cacheKey {
	h := sha256.Sum256([]byte(dir + "\x00" + pattern))
	return cacheKey(fmt.Sprintf("%x", h[:8]))
}

func (w *Workspace) GetOrLoad(dir, pattern string) (*LoadedProgram, error) {
	key := makeCacheKey(dir, pattern)

	w.mu.RLock()
	if prog, ok := w.cache[key]; ok {
		w.mu.RUnlock()
		return prog, nil
	}
	w.mu.RUnlock()

	v, err, _ := w.group.Do(string(key), func() (any, error) {
		prog, err := loadProgram(dir, pattern)
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		w.cache[key] = prog
		w.mu.Unlock()
		return prog, nil
	})
	if err != nil {
		return nil, err
	}

	return v.(*LoadedProgram), nil
}

func (w *Workspace) Invalidate(dir, pattern string) {
	key := makeCacheKey(dir, pattern)
	w.mu.Lock()
	delete(w.cache, key)
	w.mu.Unlock()
}

func (w *Workspace) Reload(dir, pattern string) (*LoadedProgram, error) {
	w.Invalidate(dir, pattern)
	return w.GetOrLoad(dir, pattern)
}

func loadProgram(dir, pattern string) (*LoadedProgram, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedTypesSizes |
			packages.NeedDeps |
			packages.NeedImports,
		Dir:   dir,
		Tests: false,
	}

	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	var errs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			errs = append(errs, fmt.Errorf("%s: %s", pkg.PkgPath, e.Msg))
		}
	}
	if len(errs) > 0 {
		for _, e := range errs {
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
			return nil, fmt.Errorf("all packages failed to load: %v", errs[0])
		}
	}

	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	allFuncs := ssautil.AllFunctions(prog)
	funcs := make([]*ssa.Function, 0, len(allFuncs))
	for fn := range allFuncs {
		funcs = append(funcs, fn)
	}

	logger.Printf("loaded %d packages, %d functions from %s:%s", len(pkgs), len(funcs), dir, pattern)

	return &LoadedProgram{
		Packages: pkgs,
		SSA:      prog,
		SSAFuncs: funcs,
	}, nil
}
