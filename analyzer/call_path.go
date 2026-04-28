package analyzer

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/callgraph"
)

const defaultCallPathMaxDepth = 8
const defaultCallPathMaxPaths = 20

// CallPath represents one function-level path from a source function to a target function.
type CallPath struct {
	Steps []string   `json:"steps"`
	Edges []CallEdge `json:"edges"`
}

// FindCallPathResult is returned by FindCallPath.
type FindCallPathResult struct {
	FromFunction string     `json:"from_function"`
	ToFunction   string     `json:"to_function"`
	Reachable    bool       `json:"reachable"`
	Paths        []CallPath `json:"paths"`
	CutoffReason string     `json:"cutoff_reason,omitempty"`
	MaxDepth     int        `json:"max_depth"`
	MaxPaths     int        `json:"max_paths"`
}

// FindCallPath performs a BFS over the CHA call graph to find call paths from
// fromFunction to toFunction, returning up to maxPaths distinct paths of length
// at most maxDepth.
func FindCallPath(ws *Workspace, dir, pattern, fromFunction, toFunction string, maxDepth, maxPaths int) (*FindCallPathResult, error) {
	if strings.TrimSpace(fromFunction) == "" {
		return nil, fmt.Errorf("from_function is required")
	}
	if strings.TrimSpace(toFunction) == "" {
		return nil, fmt.Errorf("to_function is required")
	}
	if maxDepth <= 0 || maxDepth > 12 {
		maxDepth = defaultCallPathMaxDepth
	}
	if maxPaths <= 0 || maxPaths > 100 {
		maxPaths = defaultCallPathMaxPaths
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	fromFn, err := findFunction(prog.SSAFuncs, fromFunction)
	if err != nil {
		return nil, fmt.Errorf("from_function: %w", err)
	}
	toFn, err := findFunction(prog.SSAFuncs, toFunction)
	if err != nil {
		return nil, fmt.Errorf("to_function: %w", err)
	}

	graph := prog.CallGraph()
	fromNode := graph.Nodes[fromFn]
	toNode := graph.Nodes[toFn]

	result := &FindCallPathResult{
		FromFunction: fromFn.String(),
		ToFunction:   toFn.String(),
		MaxDepth:     maxDepth,
		MaxPaths:     maxPaths,
	}

	if fromNode == nil || toNode == nil {
		return result, nil
	}

	// BFS state: each queue entry is a path of nodes and the edges traversed so far.
	type queueItem struct {
		nodes []*callgraph.Node
		edges []*callgraph.Edge
	}

	queue := []queueItem{{nodes: []*callgraph.Node{fromNode}}}
	cutoff := false

	for len(queue) > 0 {
		if len(result.Paths) >= maxPaths {
			cutoff = true
			break
		}

		item := queue[0]
		queue = queue[1:]

		current := item.nodes[len(item.nodes)-1]

		// Depth guard: steps = len(nodes)-1 edges already in this path
		if len(item.nodes)-1 >= maxDepth {
			continue
		}

		for _, edge := range current.Out {
			if edge.Callee == nil || edge.Callee.Func == nil {
				continue
			}
			// Cycle guard within this path
			inPath := false
			for _, n := range item.nodes {
				if n == edge.Callee {
					inPath = true
					break
				}
			}
			if inPath {
				continue
			}

			newNodes := append(append([]*callgraph.Node{}, item.nodes...), edge.Callee)
			newEdges := append(append([]*callgraph.Edge{}, item.edges...), edge)

			if edge.Callee == toNode {
				// Found a path
				path := buildCallPath(prog, newNodes, newEdges)
				result.Paths = append(result.Paths, path)
				result.Reachable = true
				if len(result.Paths) >= maxPaths {
					cutoff = true
					break
				}
				continue
			}

			queue = append(queue, queueItem{nodes: newNodes, edges: newEdges})
		}

		if cutoff {
			break
		}
	}

	if cutoff {
		result.CutoffReason = fmt.Sprintf("reached max_paths limit of %d", maxPaths)
	} else if !result.Reachable {
		result.CutoffReason = "no path found within depth limit"
	}

	return result, nil
}

func buildCallPath(prog *LoadedProgram, nodes []*callgraph.Node, edges []*callgraph.Edge) CallPath {
	steps := make([]string, len(nodes))
	for i, n := range nodes {
		steps[i] = n.Func.String()
	}
	callEdges := make([]CallEdge, len(edges))
	for i, e := range edges {
		callEdges[i] = toCallEdge(prog, e, i+1)
	}
	return CallPath{Steps: steps, Edges: callEdges}
}
