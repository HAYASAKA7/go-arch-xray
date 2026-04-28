package analyzer

import (
	"fmt"
	"sort"
)

// ImportCycle is a detected cycle in the package import graph. Members contains
// the package paths forming the cycle in traversal order.
type ImportCycle struct {
	Members []string `json:"members"`
}

// ImportCyclesResult is returned by DetectImportCycles.
type ImportCyclesResult struct {
	Cycles     []ImportCycle `json:"cycles"`
	CycleCount int           `json:"cycle_count"`
}

// DetectImportCycles runs a DFS over the package import graph of the loaded
// program and returns all strongly-connected components (SCCs) with more than
// one member — i.e., every actual cycle. It uses Tarjan's algorithm so the
// result is deterministic (SCCs are returned in reverse topological order).
func DetectImportCycles(ws *Workspace, dir, pattern string) (*ImportCyclesResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	// Build adjacency list from pkg.PkgPath -> []importedPkgPath, restricting
	// to packages that were loaded (not stdlib transitive deps).
	pkgByPath := make(map[string][]string)
	for _, pkg := range prog.Packages {
		neighbors := make([]string, 0, len(pkg.Imports))
		for _, imp := range pkg.Imports {
			if imp == nil {
				continue
			}
			neighbors = append(neighbors, imp.PkgPath)
		}
		sort.Strings(neighbors)
		pkgByPath[pkg.PkgPath] = neighbors
	}

	sccs := findSCCs(pkgByPath)

	result := &ImportCyclesResult{}
	for _, scc := range sccs {
		if len(scc) < 2 {
			continue
		}
		members := make([]string, len(scc))
		copy(members, scc)
		sort.Strings(members)
		result.Cycles = append(result.Cycles, ImportCycle{Members: members})
	}

	// Sort cycles for determinism
	sort.Slice(result.Cycles, func(i, j int) bool {
		if len(result.Cycles[i].Members) != len(result.Cycles[j].Members) {
			return len(result.Cycles[i].Members) < len(result.Cycles[j].Members)
		}
		return result.Cycles[i].Members[0] < result.Cycles[j].Members[0]
	})

	result.CycleCount = len(result.Cycles)
	return result, nil
}

// findSCCs implements Tarjan's SCC algorithm on an adjacency list. It returns
// all SCCs; single-node SCCs are included. Neighbors that do not exist in
// graph are silently skipped (transitive deps outside the loaded set).
func findSCCs(graph map[string][]string) [][]string {
	idx := 0
	indexMap := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	stack := []string{}
	var sccs [][]string

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indexMap[v] = idx
		lowlink[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range graph[v] {
			if _, exists := graph[w]; !exists {
				continue // skip nodes not in the graph
			}
			if _, visited := indexMap[w]; !visited {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indexMap[w] < lowlink[v] {
					lowlink[v] = indexMap[w]
				}
			}
		}

		if lowlink[v] == indexMap[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	// Use sorted keys for deterministic output
	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, v := range keys {
		if _, visited := indexMap[v]; !visited {
			strongConnect(v)
		}
	}

	return sccs
}
