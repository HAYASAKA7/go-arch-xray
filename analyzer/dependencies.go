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
	Packages []PackageDependency `json:"packages"`
}

func GetPackageDependencies(ws *Workspace, dir, pattern string, includeStdlib bool) (*DependencyResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &DependencyResult{
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
			if !includeStdlib && !rootPaths[imp.PkgPath] && isStdlib(imp.PkgPath) {
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

	return result, nil
}

func packageAnchorLocation(pkg *packages.Package) (string, int) {
	if len(pkg.CompiledGoFiles) == 0 {
		return "", 0
	}
	return pkg.CompiledGoFiles[0], 1
}
