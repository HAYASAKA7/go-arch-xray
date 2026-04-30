package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

const defaultCallersMaxDepth = 3

type CallersResult struct {
	RootFunction        string     `json:"root_function"`
	MaxDepth            int        `json:"max_depth"`
	Edges               []CallEdge `json:"edges"`
	Offset              int        `json:"offset,omitempty"`
	Limit               int        `json:"limit,omitempty"`
	MaxItems            int        `json:"max_items,omitempty"`
	ChunkSize           int        `json:"chunk_size,omitempty"`
	NextCursor          string     `json:"next_cursor,omitempty"`
	HasMore             bool       `json:"has_more,omitempty"`
	TotalBeforeTruncate int        `json:"total_before_truncate"`
	Truncated           bool       `json:"truncated"`
}

func FindCallers(ws *Workspace, dir, pattern, functionName string, maxDepth int) (*CallersResult, error) {
	return FindCallersWithOptions(ws, dir, pattern, functionName, maxDepth, QueryOptions{})
}

func FindCallersWithOptions(ws *Workspace, dir, pattern, functionName string, maxDepth int, opts QueryOptions) (*CallersResult, error) {
	if strings.TrimSpace(functionName) == "" {
		return nil, fmt.Errorf("function name is required")
	}
	if maxDepth <= 0 || maxDepth > 8 {
		maxDepth = defaultCallersMaxDepth
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

	result := &CallersResult{RootFunction: root.String(), MaxDepth: maxDepth}
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

		for _, edge := range node.In {
			if edge.Callee == nil || edge.Callee.Func == nil || edge.Caller == nil || edge.Caller.Func == nil {
				continue
			}
			key := edge.Caller.Func.String() + "->" + edge.Callee.Func.String()
			if !seenEdges[key] {
				seenEdges[key] = true
				result.Edges = append(result.Edges, toCallEdge(prog, edge, depth))
			}
			walk(edge.Caller, depth+1)
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

	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Edges, result.TotalBeforeTruncate, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Edges, "callers:"+result.RootFunction, callEdgeKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = opts.ChunkSize
	}

	return result, nil
}
