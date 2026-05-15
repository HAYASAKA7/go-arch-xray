package analyzer

import (
	"path/filepath"
	"strings"
)

func selectedPackageSet(prog *LoadedProgram, root string, patterns []string) map[string]bool {
	normalized := normalizePatternsForDir(root, patterns)
	if len(normalized) == 0 {
		return nil
	}
	out := make(map[string]bool)
	for _, pkg := range prog.Packages {
		if pkg == nil || pkg.PkgPath == "" || len(pkg.CompiledGoFiles) == 0 {
			continue
		}
		pkgDir := filepath.Dir(pkg.CompiledGoFiles[0])
		if matchesAnyScope(pkgDir, root, normalized) {
			out[pkg.PkgPath] = true
		}
	}
	return out
}

func matchesAnyScope(pkgDir, root string, patterns []string) bool {
	cleanPkg := filepath.Clean(pkgDir)
	cleanRoot := filepath.Clean(root)
	for _, pattern := range patterns {
		p := strings.TrimSpace(pattern)
		if p == "" {
			continue
		}
		if p == "./..." || p == "..." {
			return true
		}
		recursive := strings.HasSuffix(p, "/...")
		base := strings.TrimSuffix(p, "/...")
		absBase := filepath.FromSlash(base)
		if !filepath.IsAbs(absBase) {
			absBase = filepath.Join(cleanRoot, absBase)
		}
		absBase = filepath.Clean(absBase)
		if recursive {
			if cleanPkg == absBase {
				return true
			}
			rel, err := filepath.Rel(absBase, cleanPkg)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
			continue
		}
		if cleanPkg == absBase {
			return true
		}
	}
	return false
}
