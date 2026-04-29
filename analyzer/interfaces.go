package analyzer

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type Implementor struct {
	Struct  string `json:"struct"`
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Anchor  string `json:"context_anchor,omitempty"`
}

type TopologySummary struct {
	TotalImplementors int `json:"total_implementors"`
}

type TopologyResult struct {
	Interface           string           `json:"interface"`
	Implementors        []Implementor    `json:"implementors"`
	Offset              int              `json:"offset,omitempty"`
	Limit               int              `json:"limit,omitempty"`
	MaxItems            int              `json:"max_items,omitempty"`
	TotalBeforeTruncate int              `json:"total_before_truncate"`
	Truncated           bool             `json:"truncated"`
	Summary             *TopologySummary `json:"summary,omitempty"`
}

func GetInterfaceTopology(ws *Workspace, dir, pattern, ifaceName string, includeStdlib bool) (*TopologyResult, error) {
	return GetInterfaceTopologyWithOptions(ws, dir, pattern, ifaceName, includeStdlib, QueryOptions{})
}

func GetInterfaceTopologyWithOptions(ws *Workspace, dir, pattern, ifaceName string, includeStdlib bool, opts QueryOptions) (*TopologyResult, error) {
	if strings.TrimSpace(ifaceName) == "" {
		return nil, fmt.Errorf("interface name is required")
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	loaded := AllLoadedPackages(prog.Packages)

	iface, err := findInterface(loaded, prog.Packages, ifaceName)
	if err != nil {
		return nil, err
	}

	result := &TopologyResult{
		Interface:    ifaceName,
		Offset:       opts.Offset,
		Limit:        opts.Limit,
		MaxItems:     opts.MaxItems,
		Implementors: make([]Implementor, 0, 16),
	}

	for path, pkg := range loaded {
		if pkg == nil || pkg.Types == nil {
			continue
		}
		isRoot := prog.RootPaths[path]
		if !includeStdlib && !isRoot && pkg.Module == nil && isStdlib(path) {
			continue
		}
		collectImplementors(pkg, iface, &result.Implementors)
	}

	sort.Slice(result.Implementors, func(i, j int) bool {
		a, b := result.Implementors[i], result.Implementors[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Struct != b.Struct {
			return a.Struct < b.Struct
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})

	result.Implementors, result.TotalBeforeTruncate, result.Truncated = applyQueryWindow(result.Implementors, opts)
	if opts.Summary {
		result.Summary = &TopologySummary{
			TotalImplementors: result.TotalBeforeTruncate,
		}
	}

	return result, nil
}

func findInterface(loaded map[string]*packages.Package, roots []*packages.Package, name string) (*types.Interface, error) {
	if idx := strings.LastIndex(name, "."); idx > 0 && idx < len(name)-1 {
		pkgPath, typeName := name[:idx], name[idx+1:]
		if pkg, ok := loaded[pkgPath]; ok && pkg.Types != nil {
			return findInterfaceInPackage(pkg, typeName, name)
		}
		return nil, fmt.Errorf("interface %s not found in loaded packages", name)
	}

	// Unqualified: search root packages first for determinism.
	for _, pkg := range roots {
		iface, err := findInterfaceInPackage(pkg, name, name)
		if err == errInterfaceNotInPackage {
			continue
		}
		if err != nil {
			return nil, err
		}
		return iface, nil
	}
	return nil, fmt.Errorf("interface %s not found in loaded root packages; pass a fully-qualified name (pkgpath.Name) to search dependencies", name)
}

var errInterfaceNotInPackage = fmt.Errorf("interface not in package")

func findInterfaceInPackage(pkg *packages.Package, typeName, displayName string) (*types.Interface, error) {
	if pkg == nil || pkg.Types == nil {
		return nil, errInterfaceNotInPackage
	}
	scope := pkg.Types.Scope()
	obj := scope.Lookup(typeName)
	if obj == nil {
		return nil, errInterfaceNotInPackage
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, errInterfaceNotInPackage
	}
	iface, ok := tn.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", displayName)
	}
	return iface, nil
}

func collectImplementors(pkg *packages.Package, iface *types.Interface, out *[]Implementor) {
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		if _, ok := named.Underlying().(*types.Struct); !ok {
			continue
		}

		T := types.Type(named)
		ptrT := types.NewPointer(named)

		if !implementsInterface(T, iface) && !implementsInterface(ptrT, iface) {
			continue
		}

		pos := pkg.Fset.Position(obj.Pos())
		*out = append(*out, Implementor{
			Struct:  name,
			Package: pkg.PkgPath,
			File:    pos.Filename,
			Line:    pos.Line,
			Anchor:  contextAnchor(pos.Filename, pos.Line, name),
		})
	}
}

func isStdlib(pkgPath string) bool {
	for i := 0; i < len(pkgPath); i++ {
		switch pkgPath[i] {
		case '.':
			return false
		case '/':
			return true
		}
	}
	return true
}

func implementsInterface(T types.Type, iface *types.Interface) bool {
	return types.Implements(T, iface) || types.AssignableTo(T, iface)
}
