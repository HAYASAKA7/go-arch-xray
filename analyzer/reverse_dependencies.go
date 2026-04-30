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
	Offset               int                 `json:"offset,omitempty"`
	Limit                int                 `json:"limit,omitempty"`
	MaxItems             int                 `json:"max_items,omitempty"`
	ChunkSize            int                 `json:"chunk_size,omitempty"`
	NextCursor           string              `json:"next_cursor,omitempty"`
	HasMore              bool                `json:"has_more,omitempty"`
	TotalBeforeTruncate  int                 `json:"total_before_truncate"`
	Truncated            bool                `json:"truncated"`
	Diagram              string              `json:"diagram,omitempty"`
}

// FindReverseDependencies returns the set of packages (within the loaded
// program) that directly import targetPackage. When includeTransitive is true,
// it also returns the full transitive closure of dependents.
func FindReverseDependencies(ws *Workspace, dir, pattern, targetPackage string, includeTransitive bool) (*ReverseDependenciesResult, error) {
	return FindReverseDependenciesWithOptions(ws, dir, pattern, targetPackage, includeTransitive, QueryOptions{})
}

func FindReverseDependenciesWithOptions(ws *Workspace, dir, pattern, targetPackage string, includeTransitive bool, opts QueryOptions) (*ReverseDependenciesResult, error) {
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
		Offset:        opts.Offset,
		Limit:         opts.Limit,
		MaxItems:      opts.MaxItems,
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
	result.TotalBeforeTruncate = result.DirectCount

	var directTruncated bool
	var serr error
	result.DirectDependents, _, directTruncated, result.HasMore, result.NextCursor, serr = streamOrWindow(result.DirectDependents, "reverse_dependencies:"+targetPackage, reverseDependencyKey, opts)
	if serr != nil {
		return nil, serr
	}
	result.Truncated = directTruncated
	if opts.ChunkSize > 0 {
		result.ChunkSize = opts.ChunkSize
	}

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
	result.TotalBeforeTruncate += result.TransitiveCount

	// For transitive, we just apply the max items/limit constraint to avoid huge payloads,
	// ignoring offset so that we don't double-skip.
	maxItems := opts.Limit
	if maxItems == 0 || (opts.MaxItems > 0 && opts.MaxItems < maxItems) {
		maxItems = opts.MaxItems
	}
	if maxItems > 0 && len(result.TransitiveDependents) > maxItems {
		result.TransitiveDependents = result.TransitiveDependents[:maxItems]
		result.Truncated = true
	}

	if opts.Export != ExportNone {
		result.Diagram = RenderGraph(buildReverseDependencyGraph(result.TargetPackage, result.DirectDependents, result.TransitiveDependents), opts.Export)
	}

	return result, nil
}

// buildReverseDependencyGraph renders the windowed dependents pointing at the
// target package. The target node is classified "target" so renderers can
// highlight it; transitive dependents are connected through their nearest
// known intermediate when possible (and otherwise to the target directly).
func buildReverseDependencyGraph(target string, direct []ReverseDependency, transitive []string) Graph {
	b := newGraphBuilder("reverse_dependencies:"+target, "LR")
	if target != "" {
		b.addNode(target, "target")
	}
	for _, d := range direct {
		b.addEdge(d.Package, target, "", "")
	}
	// Transitive dependents are emitted as dashed edges pointing at the target
	// since the intermediate path is not retained on the result. This keeps the
	// diagram informative without overstating the precise import chain.
	for _, t := range transitive {
		b.addNode(t, "")
		b.addEdge(t, target, "transitive", "dashed")
	}
	return b.build()
}

func reverseDependencyKey(d ReverseDependency) string {
	return d.Package
}
