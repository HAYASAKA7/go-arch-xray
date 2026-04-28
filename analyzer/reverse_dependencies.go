package analyzer

import (
	"fmt"
	"sort"
)

// ReverseDependency represents a package that directly imports the target.
type ReverseDependency struct {
	Package string   `json:"package"`
	Imports []string `json:"imports,omitempty"` // other imports of this package (context)
}

// ReverseDependenciesResult is returned by FindReverseDependencies.
type ReverseDependenciesResult struct {
	TargetPackage        string              `json:"target_package"`
	DirectDependents     []ReverseDependency `json:"direct_dependents"`
	TransitiveDependents []string            `json:"transitive_dependents,omitempty"`
	DirectCount          int                 `json:"direct_count"`
	TransitiveCount      int                 `json:"transitive_count"`
}

// FindReverseDependencies returns the set of packages (within the loaded
// program) that directly import targetPackage. When includeTransitive is true,
// it also returns the full transitive closure of dependents.
func FindReverseDependencies(ws *Workspace, dir, pattern, targetPackage string, includeTransitive bool) (*ReverseDependenciesResult, error) {
	if targetPackage == "" {
		return nil, fmt.Errorf("target_package is required")
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	// Build the reverse adjacency: who imports whom.
	// reverseAdj[pkg] = list of packages that import pkg
	reverseAdj := make(map[string][]string)
	for _, pkg := range prog.Packages {
		for _, imp := range pkg.Imports {
			if imp == nil {
				continue
			}
			reverseAdj[imp.PkgPath] = append(reverseAdj[imp.PkgPath], pkg.PkgPath)
		}
	}

	result := &ReverseDependenciesResult{
		TargetPackage: targetPackage,
	}

	// Direct dependents
	directSet := make(map[string]bool)
	for _, dep := range reverseAdj[targetPackage] {
		directSet[dep] = true
	}

	for dep := range directSet {
		result.DirectDependents = append(result.DirectDependents, ReverseDependency{Package: dep})
	}
	sort.Slice(result.DirectDependents, func(i, j int) bool {
		return result.DirectDependents[i].Package < result.DirectDependents[j].Package
	})
	result.DirectCount = len(result.DirectDependents)

	if !includeTransitive {
		return result, nil
	}

	// BFS for transitive closure
	visited := make(map[string]bool)
	for dep := range directSet {
		visited[dep] = true
	}
	queue := make([]string, 0, len(directSet))
	for dep := range directSet {
		queue = append(queue, dep)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, parent := range reverseAdj[current] {
			if !visited[parent] {
				visited[parent] = true
				queue = append(queue, parent)
			}
		}
	}

	// Transitive = all visited except direct dependents
	for pkg := range visited {
		if !directSet[pkg] {
			result.TransitiveDependents = append(result.TransitiveDependents, pkg)
		}
	}
	sort.Strings(result.TransitiveDependents)
	result.TransitiveCount = len(result.TransitiveDependents)

	return result, nil
}
