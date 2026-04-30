package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const defaultCallHierarchyMaxDepth = 3

// isNotFoundError returns true for "function X not found in loaded packages"
// errors. It returns false for ambiguity errors so the fallback is not
// triggered when a name genuinely resolves to multiple candidates.
func isNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found in loaded packages")
}

// findFunctionWithFallback tries to resolve name in prog and, when not found,
// loads broader patterns (./... and go.work sub-modules) before giving up.
func findFunctionWithFallback(ws *Workspace, dir, pattern string, prog *LoadedProgram, name string) (*LoadedProgram, *ssa.Function, error) {
	fn, err := findFunction(prog, name)
	if err == nil {
		return prog, fn, nil
	}
	if !isNotFoundError(err) {
		return nil, nil, err
	}
	// Try broader fallback patterns derived from dir (./... + go.work modules)
	for _, fp := range WorkspaceFallbackPatterns(dir) {
		if fp == pattern {
			continue
		}
		broadProg, berr := ws.GetOrLoad(dir, fp)
		if berr != nil {
			continue
		}
		if fn2, ferr := findFunction(broadProg, name); ferr == nil {
			return broadProg, fn2, nil
		}
	}
	return nil, nil, err
}

type CallHierarchyResult struct {
	RootFunction        string                `json:"root_function"`
	MaxDepth            int                   `json:"max_depth"`
	Offset              int                   `json:"offset,omitempty"`
	Limit               int                   `json:"limit,omitempty"`
	MaxItems            int                   `json:"max_items,omitempty"`
	ChunkSize           int                   `json:"chunk_size,omitempty"`
	NextCursor          string                `json:"next_cursor,omitempty"`
	HasMore             bool                  `json:"has_more,omitempty"`
	TotalBeforeTruncate int                   `json:"total_before_truncate,omitempty"`
	Truncated           bool                  `json:"truncated,omitempty"`
	Summary             *CallHierarchySummary `json:"summary,omitempty"`
	Edges               []CallEdge            `json:"edges"`
	Diagram             string                `json:"diagram,omitempty"`
}

type CallHierarchySummary struct {
	TotalEdges int            `json:"total_edges"`
	ByCallType map[string]int `json:"by_call_type"`
	ByCaller   map[string]int `json:"by_caller"`
	ByCallee   map[string]int `json:"by_callee"`
}

type CallEdge struct {
	Caller   string `json:"caller"`
	Callee   string `json:"callee"`
	CallType string `json:"call_type"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Depth    int    `json:"depth"`
	Anchor   string `json:"context_anchor,omitempty"`
}

func AnalyzeCallHierarchy(ws *Workspace, dir, pattern, functionName string, maxDepth int) (*CallHierarchyResult, error) {
	return AnalyzeCallHierarchyWithOptions(ws, dir, pattern, functionName, maxDepth, QueryOptions{})
}

func AnalyzeCallHierarchyWithOptions(ws *Workspace, dir, pattern, functionName string, maxDepth int, opts QueryOptions) (*CallHierarchyResult, error) {
	if strings.TrimSpace(functionName) == "" {
		return nil, fmt.Errorf("function name is required")
	}
	if maxDepth <= 0 || maxDepth > defaultCallHierarchyMaxDepth {
		maxDepth = defaultCallHierarchyMaxDepth
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	var root *ssa.Function
	prog, root, err = findFunctionWithFallback(ws, dir, pattern, prog, functionName)
	if err != nil {
		return nil, err
	}

	graph := prog.CallGraph()
	rootNode := graph.Nodes[root]
	if rootNode == nil {
		return nil, fmt.Errorf("function %s not found in call graph", functionName)
	}

	result := &CallHierarchyResult{
		RootFunction: root.String(),
		MaxDepth:     maxDepth,
		Offset:       opts.Offset,
		Limit:        opts.Limit,
		MaxItems:     opts.MaxItems,
	}

	seenEdges := make(map[string]bool)
	seenNodes := make(map[*callgraph.Node]int)
	var walk func(*callgraph.Node, int)
	walk = func(node *callgraph.Node, depth int) {
		if depth > maxDepth {
			return
		}
		if firstDepth, ok := seenNodes[node]; ok && firstDepth <= depth {
			return
		}
		seenNodes[node] = depth

		for _, edge := range node.Out {
			if edge.Callee == nil || edge.Callee.Func == nil || edge.Caller == nil || edge.Caller.Func == nil {
				continue
			}
			key := edge.Caller.Func.String() + "->" + edge.Callee.Func.String()
			if !seenEdges[key] {
				seenEdges[key] = true
				result.Edges = append(result.Edges, toCallEdge(prog, edge, depth))
			}
			walk(edge.Callee, depth+1)
		}
	}
	walk(rootNode, 1)

	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].Depth != result.Edges[j].Depth {
			return result.Edges[i].Depth < result.Edges[j].Depth
		}
		if result.Edges[i].Caller != result.Edges[j].Caller {
			return result.Edges[i].Caller < result.Edges[j].Caller
		}
		return result.Edges[i].Callee < result.Edges[j].Callee
	})

	result.Summary = summarizeCallEdges(result.Edges, opts.Summary)

	// Apply MaxItems first as a global cap on the addressable dataset, so
	// streaming and pagination operate over the same bounded slice.
	if opts.MaxItems > 0 && len(result.Edges) > opts.MaxItems {
		result.Edges = result.Edges[:opts.MaxItems]
	}

	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
		firstKey, lastKey := callEdgeBoundaryKeys(result.Edges)
		chunk, total, nextCursor, hasMore, serr := applyStreamWindow(result.Edges, "call_hierarchy:"+result.RootFunction, firstKey, lastKey, StreamOptions{Cursor: opts.Cursor, ChunkSize: opts.ChunkSize})
		if serr != nil {
			return nil, serr
		}
		result.TotalBeforeTruncate = total
		result.Truncated = hasMore || (opts.Cursor != "")
		result.Edges = chunk
		result.NextCursor = nextCursor
		result.HasMore = hasMore
		if opts.Export != ExportNone {
			result.Diagram = RenderGraph(buildCallHierarchyGraph(result.RootFunction, result.Edges), opts.Export)
		}
		return result, nil
	}

	window, total, truncated := applyQueryWindow(result.Edges, opts)
	result.TotalBeforeTruncate = total
	result.Truncated = truncated
	result.Edges = window

	if opts.Export != ExportNone {
		result.Diagram = RenderGraph(buildCallHierarchyGraph(result.RootFunction, result.Edges), opts.Export)
	}

	return result, nil
}

// buildCallHierarchyGraph renders the windowed edge slice as a top-down call
// tree. The root function is highlighted with the "root" class so renderers
// that understand classDef can visually emphasize it.
func buildCallHierarchyGraph(root string, edges []CallEdge) Graph {
	b := newGraphBuilder("call_hierarchy:"+root, "TD")
	if root != "" {
		b.addNode(root, "root")
	}
	for _, e := range edges {
		b.addEdge(e.Caller, e.Callee, e.CallType, "")
	}
	return b.build()
}

func callEdgeBoundaryKeys(edges []CallEdge) (firstKey, lastKey string) {
	if len(edges) == 0 {
		return "", ""
	}
	return callEdgeKey(edges[0]), callEdgeKey(edges[len(edges)-1])
}

func callEdgeKey(e CallEdge) string {
	return fmt.Sprintf("%d|%s->%s|%s|%s:%d", e.Depth, e.Caller, e.Callee, e.CallType, e.File, e.Line)
}

func summarizeCallEdges(edges []CallEdge, enabled bool) *CallHierarchySummary {
	if !enabled {
		return nil
	}
	s := &CallHierarchySummary{
		TotalEdges: len(edges),
		ByCallType: make(map[string]int),
		ByCaller:   make(map[string]int),
		ByCallee:   make(map[string]int),
	}
	for _, edge := range edges {
		s.ByCallType[edge.CallType]++
		s.ByCaller[edge.Caller]++
		s.ByCallee[edge.Callee]++
	}
	return s
}

func findFunction(prog *LoadedProgram, name string) (*ssa.Function, error) {
	query := strings.TrimSpace(name)
	if query == "" {
		return nil, fmt.Errorf("function name is required")
	}

	matches := matchFunctions(prog.SSAFuncs, query)
	if len(matches) == 0 {
		matches = matchAllLoadedFunctions(prog, query)
	}
	if len(matches) == 0 {
		matches = matchFunctionsFold(prog.SSAFuncs, query)
	}
	if len(matches) == 0 {
		matches = matchAllLoadedFunctionsFold(prog, query)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("function %s not found in loaded packages", query)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	rootMatches := make([]*ssa.Function, 0, len(matches))
	for _, fn := range matches {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
			continue
		}
		if prog.RootPaths[fn.Pkg.Pkg.Path()] {
			rootMatches = append(rootMatches, fn)
		}
	}
	if len(rootMatches) == 1 {
		return rootMatches[0], nil
	}
	if len(rootMatches) > 1 {
		matches = rootMatches
	}

	nonSynthetic := make([]*ssa.Function, 0, len(matches))
	for _, fn := range matches {
		if fn == nil {
			continue
		}
		if fn.Synthetic == "" {
			nonSynthetic = append(nonSynthetic, fn)
		}
	}
	if len(nonSynthetic) == 1 {
		return nonSynthetic[0], nil
	}
	if len(nonSynthetic) > 1 {
		matches = nonSynthetic
	}

	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, fn := range matches {
			if fn != nil {
				candidates = append(candidates, fn.String())
			}
		}
		return nil, fmt.Errorf("function %s is ambiguous; qualify with package or receiver. Candidates:\n  %s",
			query, strings.Join(candidates, "\n  "))
	}
	return matches[0], nil
}

func matchFunctions(funcs []*ssa.Function, query string) []*ssa.Function {
	matches := make([]*ssa.Function, 0, 4)
	seen := make(map[*ssa.Function]bool, 4)
	for _, fn := range funcs {
		if fn == nil || seen[fn] {
			continue
		}
		if matchesFunctionName(fn, query) {
			matches = append(matches, fn)
			seen[fn] = true
		}
	}
	return matches
}

func matchAllLoadedFunctions(prog *LoadedProgram, query string) []*ssa.Function {
	all := ssautil.AllFunctions(prog.SSA)
	matches := make([]*ssa.Function, 0, 8)
	seen := make(map[*ssa.Function]bool, 8)
	for fn := range all {
		if fn == nil || seen[fn] {
			continue
		}
		if fn.Pkg == nil || fn.Pkg.Pkg == nil {
			continue
		}
		if !matchesFunctionName(fn, query) {
			continue
		}
		matches = append(matches, fn)
		seen[fn] = true
	}
	return matches
}

func matchFunctionsFold(funcs []*ssa.Function, query string) []*ssa.Function {
	matches := make([]*ssa.Function, 0, 4)
	seen := make(map[*ssa.Function]bool, 4)
	for _, fn := range funcs {
		if fn == nil || seen[fn] {
			continue
		}
		if matchesFunctionNameFold(fn, query) {
			matches = append(matches, fn)
			seen[fn] = true
		}
	}
	return matches
}

func matchAllLoadedFunctionsFold(prog *LoadedProgram, query string) []*ssa.Function {
	all := ssautil.AllFunctions(prog.SSA)
	matches := make([]*ssa.Function, 0, 8)
	seen := make(map[*ssa.Function]bool, 8)
	for fn := range all {
		if fn == nil || seen[fn] {
			continue
		}
		if fn.Pkg == nil || fn.Pkg.Pkg == nil {
			continue
		}
		if !matchesFunctionNameFold(fn, query) {
			continue
		}
		matches = append(matches, fn)
		seen[fn] = true
	}
	return matches
}

func matchesFunctionName(fn *ssa.Function, query string) bool {
	if fn == nil || query == "" {
		return false
	}

	if fn.String() == query || shortFuncName(fn.String()) == query || fn.Name() == query {
		return true
	}

	if fn.Signature == nil || fn.Signature.Recv() == nil {
		return false
	}

	recvType := fn.Signature.Recv().Type().String()
	recvNoPtr := strings.TrimPrefix(recvType, "*")
	recvShort := shortTypeName(recvNoPtr)
	methodName := fn.Name()

	candidates := []string{
		recvType + "." + methodName,
		recvNoPtr + "." + methodName,
		recvShort + "." + methodName,
		"*" + recvShort + "." + methodName,
		"(" + recvType + ")." + methodName,
		"(" + recvNoPtr + ")." + methodName,
		"(*" + recvShort + ")." + methodName,
		"(" + recvShort + ")." + methodName,
	}
	for _, candidate := range candidates {
		if candidate == query {
			return true
		}
	}

	if strings.HasSuffix(query, "."+methodName) {
		qRecv := strings.TrimSuffix(query, "."+methodName)
		qRecv = strings.Trim(qRecv, "()")
		qRecv = strings.TrimPrefix(qRecv, "*")
		qRecvShort := shortTypeName(qRecv)
		if qRecv == recvNoPtr || qRecvShort == recvShort {
			return true
		}
	}

	return false
}

// matchesFunctionNameFold is the same as matchesFunctionName but uses
// case-insensitive comparison as a fallback for tolerant lookup.
func matchesFunctionNameFold(fn *ssa.Function, query string) bool {
	if fn == nil || query == "" {
		return false
	}

	if strings.EqualFold(fn.Name(), query) ||
		strings.EqualFold(shortFuncName(fn.String()), query) ||
		strings.EqualFold(fn.String(), query) {
		return true
	}

	if fn.Signature == nil || fn.Signature.Recv() == nil {
		return false
	}

	methodName := fn.Name()
	if strings.HasSuffix(query, "."+methodName) || strings.EqualFold(strings.ToLower(methodName), strings.ToLower(query)) {
		return true
	}
	// match receiver-qualified form case-insensitively
	if dotIdx := strings.LastIndex(query, "."); dotIdx >= 0 {
		queryMethod := query[dotIdx+1:]
		if strings.EqualFold(queryMethod, methodName) {
			return true
		}
	}

	return false
}

func toCallEdge(prog *LoadedProgram, edge *callgraph.Edge, depth int) CallEdge {
	pos := edge.Pos()
	var file string
	var line int
	if pos.IsValid() {
		position := prog.SSA.Fset.Position(pos)
		file = position.Filename
		line = position.Line
	}
	caller := edge.Caller.Func.String()
	callee := edge.Callee.Func.String()
	return CallEdge{
		Caller:   caller,
		Callee:   callee,
		CallType: callType(edge.Site),
		File:     file,
		Line:     line,
		Depth:    depth,
		Anchor:   contextAnchor(file, line, shortFuncName(callee)),
	}
}

func callType(site ssa.CallInstruction) string {
	if _, ok := site.(*ssa.Go); ok {
		return "Goroutine"
	}
	if site != nil && site.Common().IsInvoke() {
		return "Interface"
	}
	return "Static"
}

func shortFuncName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 && idx < len(name)-1 {
		return name[idx+1:]
	}
	return name
}

func shortTypeName(typeName string) string {
	t := strings.TrimSpace(typeName)
	t = strings.Trim(t, "()")
	t = strings.TrimPrefix(t, "*")
	if idx := strings.LastIndex(t, "."); idx >= 0 && idx < len(t)-1 {
		return t[idx+1:]
	}
	return t
}

func contextAnchor(file string, line int, symbol string) string {
	if file == "" || line == 0 {
		return ""
	}
	return fmt.Sprintf("[Context Anchor] %s:%d %s", file, line, symbol)
}
