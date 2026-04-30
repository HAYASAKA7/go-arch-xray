package analyzer

import (
	"fmt"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// BoundaryRuleType defines how an architecture constraint is evaluated.
type BoundaryRuleType string

const (
	// RuleForbid forbids any import from packages matching From to packages matching To.
	RuleForbid BoundaryRuleType = "forbid"
	// RuleAllowOnly requires that packages matching From may only import packages
	// matching To (non-stdlib imports from other packages are violations).
	RuleAllowOnly BoundaryRuleType = "allow_only"
	// RuleAllowPrefix requires that packages matching From may only import packages
	// whose path starts with the prefix specified in To (non-stdlib imports that
	// do not have the prefix are violations).
	RuleAllowPrefix BoundaryRuleType = "allow_prefix"
)

// BoundaryRule encodes a single architecture constraint.
// From and To accept an exact package path or a path prefix ending with "/".
// For allow_prefix, To is treated as a bare prefix (strings.HasPrefix match).
type BoundaryRule struct {
	Type BoundaryRuleType `json:"type"`
	From string           `json:"from"`
	To   string           `json:"to"`
}

// BoundaryViolation records one import that breaks a rule.
type BoundaryViolation struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Import string `json:"import"`
	Rule   string `json:"rule"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Anchor string `json:"context_anchor,omitempty"`
}

// BoundaryResult is returned by CheckArchitectureBoundaries.
type BoundaryResult struct {
	Violations          []BoundaryViolation `json:"violations"`
	ViolationCount      int                 `json:"violation_count"`
	CheckedPackages     int                 `json:"checked_packages"`
	Offset              int                 `json:"offset,omitempty"`
	Limit               int                 `json:"limit,omitempty"`
	MaxItems            int                 `json:"max_items,omitempty"`
	ChunkSize           int                 `json:"chunk_size,omitempty"`
	NextCursor          string              `json:"next_cursor,omitempty"`
	HasMore             bool                `json:"has_more,omitempty"`
	TotalBeforeTruncate int                 `json:"total_before_truncate"`
	Truncated           bool                `json:"truncated"`
}

// CheckArchitectureBoundaries evaluates all packages in the loaded workspace
// against the provided ruleset and returns every import that violates a rule.
func CheckArchitectureBoundaries(ws *Workspace, dir, pattern string, rules []BoundaryRule) (*BoundaryResult, error) {
	return CheckArchitectureBoundariesWithOptions(ws, dir, pattern, rules, QueryOptions{})
}

func CheckArchitectureBoundariesWithOptions(ws *Workspace, dir, pattern string, rules []BoundaryRule, opts QueryOptions) (*BoundaryResult, error) {
	result := &BoundaryResult{
		Violations: []BoundaryViolation{},
	}
	if len(rules) == 0 {
		return result, nil
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result.CheckedPackages = len(prog.Packages)

	// inProject is the set of package paths loaded from the workspace.
	// For allow_only / allow_prefix rules, only intra-project imports are
	// evaluated so that stdlib and external dependencies are always permitted.
	inProject := make(map[string]bool, len(prog.Packages))
	for _, pkg := range prog.Packages {
		inProject[pkg.PkgPath] = true
	}

	for _, pkg := range prog.Packages {
		var locs map[string]importSourceLoc
		if prog.importLocs != nil {
			locs = prog.importLocs[pkg.PkgPath]
		}
		if locs == nil {
			// Fallback for any code path that bypasses the cache.
			locs = pkgImportLocs(pkg)
		}

		for _, rule := range rules {
			if !matchBoundaryPkg(pkg.PkgPath, rule.From) {
				continue
			}

			switch rule.Type {
			case RuleForbid:
				for impPath, loc := range locs {
					if matchBoundaryTo(impPath, rule) {
						result.Violations = append(result.Violations, newViolation(pkg.PkgPath, impPath, loc, rule))
					}
				}
			case RuleAllowOnly, RuleAllowPrefix:
				// Only evaluate imports that belong to the loaded project;
				// stdlib and external dependencies are implicitly allowed.
				for impPath, loc := range locs {
					if !inProject[impPath] {
						continue
					}
					if !matchBoundaryTo(impPath, rule) {
						result.Violations = append(result.Violations, newViolation(pkg.PkgPath, impPath, loc, rule))
					}
				}
			}
		}
	}

	sort.Slice(result.Violations, func(i, j int) bool {
		a, b := result.Violations[i], result.Violations[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Import != b.Import {
			return a.Import < b.Import
		}
		return a.Rule < b.Rule
	})

	result.ViolationCount = len(result.Violations)
	result.TotalBeforeTruncate = result.ViolationCount

	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Violations, _, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Violations, "architecture_boundaries:"+dir+"|"+pattern, boundaryViolationKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = opts.ChunkSize
	}

	return result, nil
}

func boundaryViolationKey(v BoundaryViolation) string {
	return v.From + "->" + v.Import + "|" + v.Rule + "|" + v.File + ":" + fmt.Sprintf("%d", v.Line)
}

func newViolation(from, imp string, loc importSourceLoc, rule BoundaryRule) BoundaryViolation {
	return BoundaryViolation{
		From:   from,
		To:     rule.To,
		Import: imp,
		Rule:   string(rule.Type),
		File:   loc.file,
		Line:   loc.line,
		Anchor: contextAnchor(loc.file, loc.line, imp),
	}
}

// matchBoundaryPkg returns true when pkgPath equals pattern exactly, or when
// pattern ends with "/" and pkgPath has that prefix.
func matchBoundaryPkg(pkgPath, pattern string) bool {
	if strings.HasSuffix(pattern, "/") {
		return pkgPath == strings.TrimSuffix(pattern, "/") || strings.HasPrefix(pkgPath, pattern)
	}
	return pkgPath == pattern
}

// matchBoundaryTo checks whether an import satisfies the rule's To field.
// allow_prefix uses bare strings.HasPrefix; other types use matchBoundaryPkg.
func matchBoundaryTo(impPath string, rule BoundaryRule) bool {
	if rule.Type == RuleAllowPrefix {
		return strings.HasPrefix(impPath, rule.To)
	}
	return matchBoundaryPkg(impPath, rule.To)
}

type importSourceLoc struct {
	file string
	line int
}

// pkgImportLocs builds a map from import path to its first source location.
//
// After SSA build, loadProgram sets pkg.Syntax to nil to free memory. When
// that happens we fall back to re-parsing each compiled Go file with
// parser.ImportsOnly, which is fast because it stops after the import block.
//
// In normal operation this slow path is never reached because
// CheckArchitectureBoundaries uses the importLocs cache on LoadedProgram.
func pkgImportLocs(pkg *packages.Package) map[string]importSourceLoc {
	locs := make(map[string]importSourceLoc, len(pkg.Imports))
	for path := range pkg.Imports {
		locs[path] = importSourceLoc{}
	}

	if len(pkg.Syntax) > 0 && pkg.Fset != nil {
		// Fast path: syntax tree still in memory.
		for _, file := range pkg.Syntax {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if _, ok := locs[path]; ok {
					pos := pkg.Fset.Position(imp.Pos())
					locs[path] = importSourceLoc{file: pos.Filename, line: pos.Line}
				}
			}
		}
		return locs
	}

	// Slow path: syntax was cleared after SSA build — re-parse import blocks only.
	if len(pkg.CompiledGoFiles) > 0 {
		fset := token.NewFileSet()
		for _, filename := range pkg.CompiledGoFiles {
			f, err := parser.ParseFile(fset, filename, nil, parser.ImportsOnly)
			if err != nil {
				continue
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if _, ok := locs[path]; ok {
					pos := fset.Position(imp.Pos())
					locs[path] = importSourceLoc{file: pos.Filename, line: pos.Line}
				}
			}
		}
	}
	return locs
}

// extractImportLocsFromPkg extracts import source locations using pkg.Syntax.
// This must be called before loadProgram clears pkg.Syntax.
func extractImportLocsFromPkg(pkg *packages.Package) map[string]importSourceLoc {
	locs := make(map[string]importSourceLoc, len(pkg.Imports))
	for path := range pkg.Imports {
		locs[path] = importSourceLoc{}
	}
	if len(pkg.Syntax) == 0 || pkg.Fset == nil {
		return locs
	}
	for _, file := range pkg.Syntax {
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if _, ok := locs[path]; ok {
				pos := pkg.Fset.Position(imp.Pos())
				locs[path] = importSourceLoc{file: pos.Filename, line: pos.Line}
			}
		}
	}
	return locs
}
