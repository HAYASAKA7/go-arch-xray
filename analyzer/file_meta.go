package analyzer

import (
	"os"
	"path/filepath"
	"time"

	"golang.org/x/tools/go/packages"
)

func extractFileMetasFromPackages(pkgs []*packages.Package) []FileMeta {
	metas := make([]FileMeta, 0, len(pkgs))
	now := time.Now().UTC()
	for _, pkg := range pkgs {
		if pkg == nil || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.CompiledGoFiles {
			absPath, err := filepath.Abs(file)
			if err != nil {
				absPath = file
			}
			info, statErr := os.Stat(file)
			if statErr != nil {
				info = nil
			}
			hash, hashErr := hashFile(file)
			if hashErr != nil {
				hash = ""
			}
			meta := FileMeta{
				Path:      filepath.ToSlash(absPath),
				Hash:      hash,
				Module:    modulePathFromPackage(pkg),
				Package:   pkg.PkgPath,
				IndexedAt: now,
			}
			if info != nil {
				meta.Size = info.Size()
				meta.ModifiedAt = info.ModTime().UTC()
			}
			metas = append(metas, meta)
		}
	}
	return metas
}

func modulePathFromPackage(pkg *packages.Package) string {
	if pkg == nil || pkg.Module == nil {
		return ""
	}
	return pkg.Module.Path
}
