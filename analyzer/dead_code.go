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

type DeadCodeMode string

const (
	DeadCodePrecisionMode DeadCodeMode = "precision"
	DeadCodeAuditMode     DeadCodeMode = "audit"
)

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

type DeadCodeSummary struct {
	Total               int            `json:"total"`
	Returned            int            `json:"returned"`
	ByKind              map[string]int `json:"by_kind,omitempty"`
	ByConfidence        map[string]int `json:"by_confidence,omitempty"`
	FilteredUnreachable int            `json:"filtered_unreachable,omitempty"`
	RegisteredCallbacks int            `json:"registered_callbacks,omitempty"`
}

// DeadFunction is one suspected dead function or method.
type DeadFunction struct {
	Kind                    DeadCodeKind `json:"kind"`
	Function                string       `json:"function"`
	Package                 string       `json:"package"`
	Exported                bool         `json:"exported"`
	Confidence              string       `json:"confidence,omitempty"`
	Actionability           string       `json:"actionability,omitempty"`
	InboundCallers          int          `json:"inbound_callers,omitempty"`
	ReachableFromEntrypoint bool         `json:"reachable_from_entrypoint"`
	Evidence                []string     `json:"evidence,omitempty"`
	File                    string       `json:"file,omitempty"`
	Line                    int          `json:"line,omitempty"`
	Anchor                  string       `json:"context_anchor,omitempty"`
}

// DeadCodeResult is returned by FindDeadCode.
type DeadCodeResult struct {
	Functions       []DeadFunction   `json:"functions"`
	Total           int              `json:"total"`
	IncludeExported bool             `json:"include_exported"`
	Mode            DeadCodeMode     `json:"mode,omitempty"`
	ScopePattern    string           `json:"scope_pattern,omitempty"`
	Summary         *DeadCodeSummary `json:"summary,omitempty"`
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
	// Mode controls the precision/coverage tradeoff. Precision mode returns
	// only high-confidence unreferenced symbols. Audit mode returns the full
	// static inventory with confidence labels.
	Mode DeadCodeMode
	// ScopePattern, when set, filters the reported results to packages matched
	// by the given package-pattern string while still loading the broader
	// workspace for analysis.
	ScopePattern string
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
	if dcOpts.Mode == "" {
		dcOpts.Mode = DeadCodePrecisionMode
	}
	prog, err := ws.GetOrLoadSSA(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	graph := prog.CallGraph()
	scopePkgs := selectedPackageSet(prog, dir, SplitPatterns(dcOpts.ScopePattern))

	// Step 1: find all entrypoint nodes (main + init).
	entrypointNodes := collectEntrypointNodes(prog, graph)
	callbackNodes := collectRegisteredCallbackNodes(prog, graph)
	entrypointNodes = append(entrypointNodes, callbackNodes...)

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
	summary := &DeadCodeSummary{
		ByKind:       make(map[string]int),
		ByConfidence: make(map[string]int),
	}
	for _, fn := range prog.SSAFuncs {
		if !isCandidateForDeadCheck(fn, dcOpts) {
			continue
		}
		if len(scopePkgs) > 0 && !scopePkgs[fn.Pkg.Pkg.Path()] {
			continue
		}
		if isCallbackRegisteredRoot(fn, callbackNodes) {
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
		if dcOpts.Mode != DeadCodeAuditMode && kind == DeadCodeUnreachable {
			summary.FilteredUnreachable++
			continue
		}

		file, line := ssaFuncPos(fn)
		inbound := 0
		if node != nil {
			inbound = len(node.In)
		}
		reachableFromEntrypoint := node != nil && reachable[node]
		confidence, actionability := deadFunctionMetadata(kind)
		evidence := deadFunctionEvidence(fn, kind, inbound, reachableFromEntrypoint, dcOpts.Mode, callbackNodes)
		dead = append(dead, DeadFunction{
			Kind:                    kind,
			Function:                fn.String(),
			Package:                 fn.Pkg.Pkg.Path(),
			Exported:                exported,
			Confidence:              confidence,
			Actionability:           actionability,
			InboundCallers:          inbound,
			ReachableFromEntrypoint: reachableFromEntrypoint,
			Evidence:                evidence,
			File:                    file,
			Line:                    line,
			Anchor:                  contextAnchor(file, line, fn.Name()),
		})
		summary.ByKind[string(kind)]++
		summary.ByConfidence[confidence]++
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
		IncludeExported: dcOpts.IncludeExported,
		Mode:            dcOpts.Mode,
		ScopePattern:    dcOpts.ScopePattern,
		Summary:         summary,
		Notes: []string{
			"CHA call graph is sound for static and interface dispatch but cannot see reflection, plugin loading, cgo, or //go:linkname callers. Verify before deleting.",
			"Methods satisfying interfaces consumed by other modules may appear dead even when called externally.",
			"Test files (*_test.go) are not loaded into the analysis program; functions only used from tests cannot be detected.",
			"Known callback registrations such as MCP tool handlers are treated as roots to reduce false positives.",
		},
	}
	if !dcOpts.IncludeExported {
		result.Notes = append(result.Notes, "Exported symbols are excluded by default; pass include_exported=true to audit public API.")
	}
	summary.Total = len(dead) + summary.FilteredUnreachable
	summary.Returned = len(dead)
	summary.RegisteredCallbacks = len(callbackNodes)
	result.Total = summary.Total

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

func deadFunctionMetadata(kind DeadCodeKind) (confidence, actionability string) {
	switch kind {
	case DeadCodeUnreferenced:
		return "high", "candidate_for_delete"
	case DeadCodeUnreachable:
		return "medium", "verify_before_delete"
	default:
		return "unknown", "review"
	}
}

func deadFunctionEvidence(fn *ssa.Function, kind DeadCodeKind, inbound int, reachableFromEntrypoint bool, mode DeadCodeMode, callbackNodes []*callgraph.Node) []string {
	evidence := []string{
		fmt.Sprintf("inbound_callers=%d", inbound),
		fmt.Sprintf("reachable_from_entrypoint=%t", reachableFromEntrypoint),
	}
	if kind == DeadCodeUnreachable {
		evidence = append(evidence, "caller_chain does not reach a discovered entrypoint")
	}
	if mode == DeadCodeAuditMode {
		evidence = append(evidence, "audit_mode=full_static_inventory")
	}
	if fn != nil && isCallbackRegisteredRoot(fn, callbackNodes) {
		evidence = append(evidence, "callback_registered_root=true")
	}
	return evidence
}

func isCallbackRegisteredRoot(fn *ssa.Function, nodes []*callgraph.Node) bool {
	if fn == nil {
		return false
	}
	for _, n := range nodes {
		if n != nil && n.Func == fn {
			return true
		}
	}
	return false
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

func collectRegisteredCallbackNodes(prog *LoadedProgram, graph *callgraph.Graph) []*callgraph.Node {
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
		if fn == nil || fn.Blocks == nil {
			continue
		}
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				if !isCallbackRegistrationCall(call) {
					continue
				}
				if callee := callbackHandlerFunction(call.Common()); callee != nil {
					add(callee)
				}
			}
		}
	}
	return out
}

func isCallbackRegistrationCall(call *ssa.Call) bool {
	if call == nil || call.Common() == nil {
		return false
	}
	if !strings.Contains(call.Common().String(), "AddTool") {
		return false
	}
	return callbackHandlerFunction(call.Common()) != nil
}

func callbackHandlerFunction(common *ssa.CallCommon) *ssa.Function {
	if common == nil {
		return nil
	}
	for _, arg := range common.Args {
		switch v := arg.(type) {
		case *ssa.Function:
			if v != nil {
				return v
			}
		case *ssa.MakeClosure:
			if fn, ok := v.Fn.(*ssa.Function); ok {
				return fn
			}
		}
	}
	if callee := common.StaticCallee(); callee != nil {
		if strings.Contains(callee.String(), "AddTool") {
			for _, arg := range common.Args {
				if fn, ok := arg.(*ssa.Function); ok {
					return fn
				}
			}
		}
	}
	return nil
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
