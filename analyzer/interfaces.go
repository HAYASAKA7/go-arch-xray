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

type TopologyResult struct {
	Interface    string        `json:"interface"`
	Implementors []Implementor `json:"implementors"`
}

func GetInterfaceTopology(ws *Workspace, dir, pattern, ifaceName string, includeStdlib bool) (*TopologyResult, error) {
	if strings.TrimSpace(ifaceName) == "" {
		return nil, fmt.Errorf("interface name is required")
	}
	if strings.TrimSpace(pattern) == "" {
		pattern = "./..."
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	iface, err := findInterface(prog.Packages, ifaceName)
	if err != nil {
		return nil, err
	}

	result := &TopologyResult{Interface: ifaceName}

	rootPaths := make(map[string]bool, len(prog.Packages))
	for _, pkg := range prog.Packages {
		rootPaths[pkg.PkgPath] = true
	}

	seen := make(map[string]bool)
	var walk func([]*packages.Package)
	walk = func(pkgs []*packages.Package) {
		for _, pkg := range pkgs {
			if seen[pkg.PkgPath] {
				continue
			}
			seen[pkg.PkgPath] = true

			if !includeStdlib && !rootPaths[pkg.PkgPath] && isStdlib(pkg.PkgPath) {
				continue
			}

			collectImplementors(pkg, iface, &result.Implementors)

			for _, imp := range pkg.Imports {
				walk([]*packages.Package{imp})
			}
		}
	}
	walk(prog.Packages)

	sort.Slice(result.Implementors, func(i, j int) bool {
		if result.Implementors[i].Package != result.Implementors[j].Package {
			return result.Implementors[i].Package < result.Implementors[j].Package
		}
		if result.Implementors[i].Struct != result.Implementors[j].Struct {
			return result.Implementors[i].Struct < result.Implementors[j].Struct
		}
		if result.Implementors[i].File != result.Implementors[j].File {
			return result.Implementors[i].File < result.Implementors[j].File
		}
		return result.Implementors[i].Line < result.Implementors[j].Line
	})

	return result, nil
}

func findInterface(pkgs []*packages.Package, name string) (*types.Interface, error) {
	if idx := strings.LastIndex(name, "."); idx > 0 && idx < len(name)-1 {
		pkgPath, typeName := name[:idx], name[idx+1:]
		for _, pkg := range pkgs {
			if pkg.PkgPath != pkgPath {
				continue
			}
			return findInterfaceInPackage(pkg, typeName, name)
		}
		return nil, fmt.Errorf("interface %s not found in loaded packages", name)
	}

	for _, pkg := range pkgs {
		iface, err := findInterfaceInPackage(pkg, name, name)
		if err == errInterfaceNotInPackage {
			continue
		}
		if err != nil {
			return nil, err
		}
		return iface, nil
	}
	return nil, fmt.Errorf("interface %s not found in loaded packages", name)
}

var errInterfaceNotInPackage = fmt.Errorf("interface not in package")

func findInterfaceInPackage(pkg *packages.Package, typeName, displayName string) (*types.Interface, error) {
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

		T := named
		ptrT := types.NewPointer(T)

		if implementsInterface(T, iface) || implementsInterface(ptrT, iface) {
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
}

func implementsInterface(T types.Type, iface *types.Interface) bool {
	return types.Implements(T, iface) || types.AssignableTo(T, iface)
}

func isStdlib(pkgPath string) bool {
	for i := 0; i < len(pkgPath); i++ {
		if pkgPath[i] == '.' {
			return false
		}
		if pkgPath[i] == '/' {
			return true
		}
	}
	return true
}
