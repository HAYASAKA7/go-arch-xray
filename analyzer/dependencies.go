package analyzer

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/packages"
)

type PackageDependency struct {
	Package string   `json:"package"`
	Imports []string `json:"imports"`
	File    string   `json:"file,omitempty"`
	Line    int      `json:"line,omitempty"`
	Anchor  string   `json:"context_anchor,omitempty"`
}

type DependencyResult struct {
	Offset              int                 `json:"offset,omitempty"`
	Limit               int                 `json:"limit,omitempty"`
	MaxItems            int                 `json:"max_items,omitempty"`
	ChunkSize           int                 `json:"chunk_size,omitempty"`
	NextCursor          string              `json:"next_cursor,omitempty"`
	HasMore             bool                `json:"has_more,omitempty"`
	TotalBeforeTruncate int                 `json:"total_before_truncate,omitempty"`
	Truncated           bool                `json:"truncated,omitempty"`
	Summary             *DependencySummary  `json:"summary,omitempty"`
	Packages            []PackageDependency `json:"packages"`
	Diagram             string              `json:"diagram,omitempty"`
}

type DependencySummary struct {
	TotalPackages int            `json:"total_packages"`
	TotalImports  int            `json:"total_imports"`
	ByPackage     map[string]int `json:"imports_by_package"`
}

func GetPackageDependencies(ws *Workspace, dir, pattern string, includeStdlib bool) (*DependencyResult, error) {
	return GetPackageDependenciesWithOptions(ws, dir, pattern, includeStdlib, QueryOptions{})
}

func GetPackageDependenciesWithOptions(ws *Workspace, dir, pattern string, includeStdlib bool, opts QueryOptions) (*DependencyResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &DependencyResult{
		Offset:   opts.Offset,
		Limit:    opts.Limit,
		MaxItems: opts.MaxItems,
		Packages: make([]PackageDependency, 0, len(prog.Packages)),
	}

	rootPaths := make(map[string]bool, len(prog.Packages))
	for _, pkg := range prog.Packages {
		rootPaths[pkg.PkgPath] = true
	}

	for _, pkg := range prog.Packages {
		imports := make([]string, 0, len(pkg.Imports))
		seen := make(map[string]bool, len(pkg.Imports))
		for _, imp := range pkg.Imports {
			if imp == nil || imp.PkgPath == "" {
				continue
			}
			if !includeStdlib && !rootPaths[imp.PkgPath] && imp.Module == nil && isStdlib(imp.PkgPath) {
				continue
			}
			if seen[imp.PkgPath] {
				continue
			}
			seen[imp.PkgPath] = true
			imports = append(imports, imp.PkgPath)
		}
		sort.Strings(imports)

		file, line := packageAnchorLocation(pkg)
		result.Packages = append(result.Packages, PackageDependency{
			Package: pkg.PkgPath,
			Imports: imports,
			File:    file,
			Line:    line,
			Anchor:  contextAnchor(file, line, pkg.PkgPath),
		})
	}

	sort.Slice(result.Packages, func(i, j int) bool {
		return result.Packages[i].Package < result.Packages[j].Package
	})

	result.Summary = summarizeDependencies(result.Packages, opts.Summary)
	window, total, truncated, hasMore, nextCursor, serr := streamOrWindow(result.Packages, "package_dependencies:"+dir+"|"+pattern, packageDependencyKey, opts)
	if serr != nil {
		return nil, serr
	}
	result.TotalBeforeTruncate = total
	result.Truncated = truncated
	result.Packages = window
	result.HasMore = hasMore
	result.NextCursor = nextCursor
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	if opts.Export != ExportNone {
		result.Diagram = RenderGraph(buildDependencyGraph(result.Packages), opts.Export)
	}

	return result, nil
}

// buildDependencyGraph constructs a directed graph where each package points
// at every package it imports. Only the packages and imports present in the
// supplied window are rendered, so the diagram respects the same pagination
// limits the JSON payload reports.
func buildDependencyGraph(pkgs []PackageDependency) Graph {
	b := newGraphBuilder("package_dependencies", "LR")
	for _, pkg := range pkgs {
		b.addNode(pkg.Package, "")
	}
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			b.addEdge(pkg.Package, imp, "", "")
		}
	}
	return b.build()
}

func packageDependencyKey(p PackageDependency) string {
	return p.Package
}

func summarizeDependencies(packages []PackageDependency, enabled bool) *DependencySummary {
	if !enabled {
		return nil
	}
	summary := &DependencySummary{
		TotalPackages: len(packages),
		ByPackage:     make(map[string]int, len(packages)),
	}
	for _, pkg := range packages {
		summary.TotalImports += len(pkg.Imports)
		summary.ByPackage[pkg.Package] = len(pkg.Imports)
	}
	return summary
}

func packageAnchorLocation(pkg *packages.Package) (string, int) {
	if len(pkg.CompiledGoFiles) == 0 {
		return "", 0
	}
	return pkg.CompiledGoFiles[0], 1
}
