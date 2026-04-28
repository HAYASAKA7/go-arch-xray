package analyzer

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// EntrypointKind classifies an entrypoint.
type EntrypointKind string

const (
	EntrypointMain      EntrypointKind = "main"
	EntrypointInit      EntrypointKind = "init"
	EntrypointGoroutine EntrypointKind = "goroutine_start"
)

// Entrypoint describes a detected program entrypoint.
type Entrypoint struct {
	Kind     EntrypointKind `json:"kind"`
	Function string         `json:"function"`
	Package  string         `json:"package"`
	File     string         `json:"file,omitempty"`
	Line     int            `json:"line,omitempty"`
	Anchor   string         `json:"context_anchor,omitempty"`
}

// EntrypointsResult is returned by ListEntrypoints.
type EntrypointsResult struct {
	Entrypoints []Entrypoint `json:"entrypoints"`
	Total       int          `json:"total"`
}

// ListEntrypoints scans the loaded SSA program for main functions, init
// functions, and goroutine-spawn sites (go statements), returning each as an
// Entrypoint with source location.
func ListEntrypoints(ws *Workspace, dir, pattern string) (*EntrypointsResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &EntrypointsResult{
		Entrypoints: []Entrypoint{},
	}

	seenFunc := make(map[string]bool)

	// Pass 1: main and init functions.
	for _, fn := range prog.SSAFuncs {
		if fn == nil || fn.Package() == nil || fn.Package().Pkg == nil {
			continue
		}
		pkgPath := fn.Package().Pkg.Path()
		pkgName := fn.Package().Pkg.Name()

		var kind EntrypointKind
		switch {
		case fn.Name() == "main" && pkgName == "main":
			kind = EntrypointMain
		case fn.Name() == "init":
			kind = EntrypointInit
		default:
			continue
		}

		key := string(kind) + ":" + fn.String()
		if seenFunc[key] {
			continue
		}
		seenFunc[key] = true

		file, line := ssaFuncPos(fn)
		result.Entrypoints = append(result.Entrypoints, Entrypoint{
			Kind:     kind,
			Function: fn.String(),
			Package:  pkgPath,
			File:     file,
			Line:     line,
			Anchor:   contextAnchor(file, line, fn.Name()),
		})
	}

	// Pass 2: goroutine spawn sites (go statements).
	seenGo := make(map[string]bool)
	for _, fn := range prog.SSAFuncs {
		if fn == nil || fn.Blocks == nil || fn.Package() == nil || fn.Package().Pkg == nil {
			continue
		}
		pkgPath := fn.Package().Pkg.Path()

		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				goInstr, ok := instr.(*ssa.Go)
				if !ok {
					continue
				}

				spawned := goroutineTarget(goInstr, fn)
				goKey := pkgPath + ":" + spawned
				if seenGo[goKey] {
					continue
				}
				seenGo[goKey] = true

				pos := fn.Prog.Fset.Position(goInstr.Pos())
				result.Entrypoints = append(result.Entrypoints, Entrypoint{
					Kind:     EntrypointGoroutine,
					Function: spawned,
					Package:  pkgPath,
					File:     pos.Filename,
					Line:     pos.Line,
					Anchor:   contextAnchor(pos.Filename, pos.Line, spawned),
				})
			}
		}
	}

	sort.Slice(result.Entrypoints, func(i, j int) bool {
		a, b := result.Entrypoints[i], result.Entrypoints[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Function < b.Function
	})

	result.Total = len(result.Entrypoints)
	return result, nil
}

func ssaFuncPos(fn *ssa.Function) (string, int) {
	if fn.Prog == nil {
		return "", 0
	}
	pos := fn.Prog.Fset.Position(fn.Pos())
	return pos.Filename, pos.Line
}

func goroutineTarget(g *ssa.Go, enclosing *ssa.Function) string {
	switch callee := g.Call.Value.(type) {
	case *ssa.Function:
		return callee.String()
	case *ssa.MakeClosure:
		if fn, ok := callee.Fn.(*ssa.Function); ok {
			return fn.String()
		}
		return "<closure in " + enclosing.String() + ">"
	default:
		return "<dynamic in " + enclosing.String() + ">"
	}
}
