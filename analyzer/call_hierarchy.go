package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/ssa"
)

const defaultCallHierarchyMaxDepth = 3

type CallHierarchyResult struct {
	RootFunction string     `json:"root_function"`
	MaxDepth     int        `json:"max_depth"`
	Edges        []CallEdge `json:"edges"`
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
	if strings.TrimSpace(functionName) == "" {
		return nil, fmt.Errorf("function name is required")
	}
	if strings.TrimSpace(pattern) == "" {
		pattern = "./..."
	}
	if maxDepth <= 0 || maxDepth > defaultCallHierarchyMaxDepth {
		maxDepth = defaultCallHierarchyMaxDepth
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	root, err := findFunction(prog.SSAFuncs, functionName)
	if err != nil {
		return nil, err
	}

	graph := cha.CallGraph(prog.SSA)
	rootNode := graph.Nodes[root]
	if rootNode == nil {
		return nil, fmt.Errorf("function %s not found in call graph", functionName)
	}

	result := &CallHierarchyResult{
		RootFunction: root.String(),
		MaxDepth:     maxDepth,
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

	return result, nil
}

func findFunction(funcs []*ssa.Function, name string) (*ssa.Function, error) {
	var matches []*ssa.Function
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		if fn.String() == name || shortFuncName(fn.String()) == name {
			matches = append(matches, fn)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("function %s not found in loaded packages", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("function %s is ambiguous; use a package-qualified function name", name)
	}
	return matches[0], nil
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

func contextAnchor(file string, line int, symbol string) string {
	if file == "" || line == 0 {
		return ""
	}
	return fmt.Sprintf("[Context Anchor] %s:%d %s", file, line, symbol)
}
