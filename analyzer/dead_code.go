package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

// DeadCodeKind classifies how confident the analyzer is that a symbol is dead.
type DeadCodeKind string

const (
	// DeadCodeUnreferenced means the symbol has zero inbound edges in the
	// CHA call graph and is not reachable from any entrypoint (main, init,
	// goroutine spawn). Highest confidence.
	DeadCodeUnreferenced DeadCodeKind = "unreferenced"
	// DeadCodeUnreachable means the symbol has inbound edges but every
	// caller chain dies before reaching an entrypoint. Medium confidence:
	// could be reached via reflection, plugins, or test-only code paths
	// that aren't part of the loaded program.
	DeadCodeUnreachable DeadCodeKind = "unreachable_from_entrypoint"
)

// DeadFunction is one suspected dead function or method.
type DeadFunction struct {
	Kind     DeadCodeKind `json:"kind"`
	Function string       `json:"function"`
	Package  string       `json:"package"`
	Exported bool         `json:"exported"`
	File     string       `json:"file,omitempty"`
	Line     int          `json:"line,omitempty"`
	Anchor   string       `json:"context_anchor,omitempty"`
}

// DeadCodeResult is returned by FindDeadCode.
type DeadCodeResult struct {
	Functions       []DeadFunction `json:"functions"`
	Total           int            `json:"total"`
	IncludeExported bool           `json:"include_exported"`
	// Notes carries caveats/limitations the AI client should propagate to
	// the user (e.g. interface dispatch and reflection blind spots).
	Notes []string `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

// DeadCodeOptions tunes the dead-code scan.
type DeadCodeOptions struct {
	// IncludeExported reports unreferenced exported symbols as well. By
	// default exported symbols are excluded because they may be public API
	// consumed by other modules. Library authors auditing their own public
	// surface should set this to true.
	IncludeExported bool
}

// FindDeadCode reports functions and methods in the loaded program that have
// no inbound callers in the CHA call graph and are unreachable from any
// program entrypoint. CHA accounts for interface dispatch, but reflection,
// linkname, plugin loading, and cgo invocations are blind spots — the result
// is best-effort and surfaces those caveats via Notes.
func FindDeadCode(ws *Workspace, dir, pattern string) (*DeadCodeResult, error) {
	return FindDeadCodeWithOptions(ws, dir, pattern, DeadCodeOptions{}, QueryOptions{})
}

// FindDeadCodeWithOptions is the streaming/paginated variant.
func FindDeadCodeWithOptions(ws *Workspace, dir, pattern string, dcOpts DeadCodeOptions, opts QueryOptions) (*DeadCodeResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	graph := prog.CallGraph()

	// Step 1: find all entrypoint nodes (main + init).
	entrypointNodes := collectEntrypointNodes(prog, graph)

	// Step 2: forward-reachability from entrypoints over the call graph.
	reachable := make(map[*callgraph.Node]bool, len(graph.Nodes))
	var visit func(*callgraph.Node)
	visit = func(n *callgraph.Node) {
		if n == nil || reachable[n] {
			return
		}
		reachable[n] = true
		for _, e := range n.Out {
			visit(e.Callee)
		}
	}
	for _, n := range entrypointNodes {
		visit(n)
	}

	// Step 3: classify root functions.
	dead := make([]DeadFunction, 0, 32)
	for _, fn := range prog.SSAFuncs {
		if !isCandidateForDeadCheck(fn, dcOpts) {
			continue
		}
		node := graph.Nodes[fn]
		exported := isExportedFunc(fn)
		if !dcOpts.IncludeExported && exported {
			continue
		}

		var kind DeadCodeKind
		switch {
		case node == nil || len(node.In) == 0:
			kind = DeadCodeUnreferenced
		case !reachable[node]:
			kind = DeadCodeUnreachable
		default:
			continue
		}

		file, line := ssaFuncPos(fn)
		dead = append(dead, DeadFunction{
			Kind:     kind,
			Function: fn.String(),
			Package:  fn.Pkg.Pkg.Path(),
			Exported: exported,
			File:     file,
			Line:     line,
			Anchor:   contextAnchor(file, line, fn.Name()),
		})
	}

	sort.Slice(dead, func(i, j int) bool {
		if dead[i].Kind != dead[j].Kind {
			return dead[i].Kind < dead[j].Kind
		}
		if dead[i].Package != dead[j].Package {
			return dead[i].Package < dead[j].Package
		}
		return dead[i].Function < dead[j].Function
	})

	result := &DeadCodeResult{
		Functions:       dead,
		Total:           len(dead),
		IncludeExported: dcOpts.IncludeExported,
		Notes: []string{
			"CHA call graph is sound for static and interface dispatch but cannot see reflection, plugin loading, cgo, or //go:linkname callers. Verify before deleting.",
			"Methods satisfying interfaces consumed by other modules may appear dead even when called externally.",
			"Test files (*_test.go) are not loaded into the analysis program; functions only used from tests cannot be detected.",
		},
	}
	if !dcOpts.IncludeExported {
		result.Notes = append(result.Notes, "Exported symbols are excluded by default; pass include_exported=true to audit public API.")
	}

	result.TotalBeforeTruncate = result.Total
	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Functions, _, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Functions, "dead_code:"+dir+"|"+pattern, deadFunctionKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	return result, nil
}

func deadFunctionKey(d DeadFunction) string {
	return string(d.Kind) + "|" + d.Package + "|" + d.Function + "|" + d.File + ":" + fmt.Sprintf("%d", d.Line)
}

// collectEntrypointNodes returns the set of SSA functions that should seed
// reachability analysis: main functions, package init functions, and any
// function spawned via a `go` statement (because those run independently of
// their syntactic caller chain).
func collectEntrypointNodes(prog *LoadedProgram, graph *callgraph.Graph) []*callgraph.Node {
	out := make([]*callgraph.Node, 0, 16)
	seen := make(map[*callgraph.Node]bool)

	add := func(fn *ssa.Function) {
		if fn == nil {
			return
		}
		node := graph.Nodes[fn]
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		out = append(out, node)
	}

	for _, fn := range prog.SSAFuncs {
		if fn == nil || fn.Package() == nil || fn.Package().Pkg == nil {
			continue
		}
		pkgName := fn.Package().Pkg.Name()
		if (fn.Name() == "main" && pkgName == "main") || fn.Name() == "init" {
			add(fn)
			continue
		}

		// goroutine spawn targets are independent entrypoints.
		if fn.Blocks == nil {
			continue
		}
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				goInstr, ok := instr.(*ssa.Go)
				if !ok {
					continue
				}
				switch callee := goInstr.Call.Value.(type) {
				case *ssa.Function:
					add(callee)
				case *ssa.MakeClosure:
					if inner, ok := callee.Fn.(*ssa.Function); ok {
						add(inner)
					}
				}
			}
		}
	}
	return out
}

// isCandidateForDeadCheck filters out SSA functions that should not appear
// in the dead-code report at all (synthetic wrappers, anonymous closures,
// init/main themselves, generic instantiations whose origin is also reported).
func isCandidateForDeadCheck(fn *ssa.Function, dcOpts DeadCodeOptions) bool {
	if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
		return false
	}
	if fn.Synthetic != "" {
		return false
	}
	if fn.Parent() != nil {
		// Anonymous closure inside another function — its enclosing
		// function carries the meaningful liveness signal.
		return false
	}
	name := fn.Name()
	if name == "" || name == "init" || name == "main" {
		return false
	}
	// Skip generic origin's instantiations; the origin is the user-facing
	// declaration and CHA reports both.
	if fn.Origin() != nil && fn.Origin() != fn {
		return false
	}
	return true
}

// isExportedFunc reports whether the SSA function corresponds to an exported
// symbol — for methods, both the method name AND the receiver type must be
// exported for the method to be reachable from outside the package.
func isExportedFunc(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	name := fn.Name()
	if name == "" || !isExportedName(name) {
		return false
	}
	if recv := fn.Signature.Recv(); recv != nil {
		typeStr := recv.Type().String()
		// Trim leading "*" for pointer receivers.
		typeStr = strings.TrimPrefix(typeStr, "*")
		// Take the final type-name segment after the last "/" then "."
		if idx := strings.LastIndex(typeStr, "."); idx >= 0 {
			typeStr = typeStr[idx+1:]
		}
		// Strip any generic type parameter brackets.
		if idx := strings.Index(typeStr, "["); idx >= 0 {
			typeStr = typeStr[:idx]
		}
		if !isExportedName(typeStr) {
			return false
		}
	}
	return true
}

func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := name[0]
	return r >= 'A' && r <= 'Z'
}
