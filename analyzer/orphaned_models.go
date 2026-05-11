package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// OrphanedModelReason classifies why a model is considered orphaned.
type OrphanedModelReason string

const (
	// OrphanedNoReferences means the model type has no references in the program.
	OrphanedNoReferences OrphanedModelReason = "no_references"
	// OrphanedNoOrmUsage means the model has references but no ORM/database operations.
	OrphanedNoOrmUsage OrphanedModelReason = "no_orm_usage"
)

// OrphanedModel is a database model that appears unused.
type OrphanedModel struct {
	Name         string              `json:"name"`
	Package      string              `json:"package"`
	File         string              `json:"file"`
	Line         int                 `json:"line"`
	Anchor       string              `json:"context_anchor,omitempty"`
	ORMFramework string              `json:"orm_framework"`
	Reason       OrphanedModelReason `json:"reason"`
	Notes        string              `json:"notes,omitempty"`
}

// OrphanedModelResult is returned by FindOrphanedDatabaseModels.
type OrphanedModelResult struct {
	Models       []OrphanedModel `json:"models"`
	Total        int             `json:"total"`
	ORMFramework string          `json:"orm_framework"`
	Scanned      int             `json:"scanned_models"`
	Notes        []string        `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

// OrphanedModelOptions tunes the orphaned model scan.
type OrphanedModelOptions struct {
	// ORMFramework specifies which ORM to detect. Currently only "gorm" is supported.
	ORMFramework string
}

// FindOrphanedDatabaseModels reports database models that are defined but never
// used in queries or migrations. Currently only supports GORM (gorm:"..." tagged structs).
func FindOrphanedDatabaseModels(ws *Workspace, dir, pattern string, opts OrphanedModelOptions) (*OrphanedModelResult, error) {
	return FindOrphanedDatabaseModelsWithOptions(ws, dir, pattern, opts, QueryOptions{})
}

// FindOrphanedDatabaseModelsWithOptions is the streaming/paginated variant.
func FindOrphanedDatabaseModelsWithOptions(ws *Workspace, dir, pattern string, oOpts OrphanedModelOptions, qOpts QueryOptions) (*OrphanedModelResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	// Default to GORM if not specified
	framework := oOpts.ORMFramework
	if framework == "" {
		framework = "gorm"
	}

	if framework != "gorm" {
		return nil, fmt.Errorf("unsupported ORM framework: %s (currently only 'gorm' is supported)", framework)
	}

	// Use cached GORM models extracted during load
	cachedModels := prog.gormModels
	if cachedModels == nil {
		cachedModels = []GormModel{}
	}

	// Convert cached models to a more detailed format for analysis
	type detailedModel struct {
		GormModel
		SSAType types.Type
	}

	detailed := make([]detailedModel, 0, len(cachedModels))
	for _, gm := range cachedModels {
		typ, err := findModelType(prog, gm)
		if err != nil {
			continue // Skip if we can't find the type
		}
		detailed = append(detailed, detailedModel{
			GormModel: gm,
			SSAType:   typ,
		})
	}

	// Check each model for database usage
	orphaned := make([]OrphanedModel, 0, len(detailed)/4) // Assume ~25% are orphaned
	for _, model := range detailed {
		refs := countStructReferences(prog.SSAFuncs, model.SSAType)
		ormUsage := hasOrmUsage(prog.SSAFuncs, model.SSAType)

		var reason OrphanedModelReason
		var notes string

		switch {
		case refs == 0 && !ormUsage:
			reason = OrphanedNoReferences
			notes = "no references to this struct type found in the program"
		case refs > 0 && !ormUsage:
			reason = OrphanedNoOrmUsage
			notes = fmt.Sprintf("found %d reference(s) but no ORM/database operations", refs)
		default:
			continue // Model is used, not orphaned
		}

		orphaned = append(orphaned, OrphanedModel{
			Name:         model.Name,
			Package:      model.Pkg,
			File:         model.File,
			Line:         model.Line,
			Anchor:       contextAnchor(model.File, model.Line, model.Name),
			ORMFramework: framework,
			Reason:       reason,
			Notes:        notes,
		})
	}

	sort.Slice(orphaned, func(i, j int) bool {
		if orphaned[i].Package != orphaned[j].Package {
			return orphaned[i].Package < orphaned[j].Package
		}
		return orphaned[i].Name < orphaned[j].Name
	})

	result := &OrphanedModelResult{
		Models:       orphaned,
		Total:        len(orphaned),
		ORMFramework: framework,
		Scanned:      len(cachedModels),
		Notes: []string{
			"Models detected by GORM tag presence (gorm:\"...\") only.",
			"Reflection-based patterns and raw SQL queries are not detected.",
			"Test-only models may appear orphaned if test packages are not included in the scan pattern.",
		},
	}

	result.TotalBeforeTruncate = result.Total
	result.Offset = qOpts.Offset
	result.Limit = qOpts.Limit
	result.MaxItems = qOpts.MaxItems

	result.Models, _, result.Truncated, result.HasMore, result.NextCursor, err = streamOrWindow(result.Models, "orphaned_models:"+dir+"|"+pattern, orphanedModelKey, qOpts)
	if err != nil {
		return nil, err
	}
	if qOpts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(qOpts.ChunkSize)
	}

	return result, nil
}

func orphanedModelKey(m OrphanedModel) string {
	return m.Package + "|" + m.Name + "|" + m.File + ":" + fmt.Sprintf("%d", m.Line)
}

// findModelType finds the types.Type for a GORMModel by looking it up in the package.
func findModelType(prog *LoadedProgram, gm GormModel) (types.Type, error) {
	for _, pkg := range prog.Packages {
		if pkg == nil || pkg.Types == nil || pkg.PkgPath != gm.Pkg {
			continue
		}
		scope := pkg.Types.Scope()
		obj := scope.Lookup(gm.Name)
		if obj == nil {
			continue
		}
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		return tn.Type(), nil
	}
	return nil, fmt.Errorf("model type not found: %s.%s", gm.Pkg, gm.Name)
}

// countStructReferences counts how many times the struct type is referenced.
func countStructReferences(funcs []*ssa.Function, targetType types.Type) int {
	count := 0

	for _, fn := range funcs {
		if fn == nil || fn.Signature == nil {
			continue
		}

		// Check function parameters and return types
		if referencesType(fn.Signature, targetType) {
			count++
			continue
		}

		// Check function body for references
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instructionReferencesType(instr, targetType) {
					count++
					break // Only count once per function
				}
			}
		}
	}

	return count
}

// referencesType checks if a signature references the given type.
func referencesType(sig *types.Signature, targetType types.Type) bool {
	// Check parameters
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if types.Identical(params.At(i).Type(), targetType) {
			return true
		}
		// Check pointer to type
		if ptr, ok := params.At(i).Type().(*types.Pointer); ok && types.Identical(ptr.Elem(), targetType) {
			return true
		}
		// Check slice of type
		if slice, ok := params.At(i).Type().(*types.Slice); ok && types.Identical(slice.Elem(), targetType) {
			return true
		}
	}

	// Check return types
	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		if types.Identical(results.At(i).Type(), targetType) {
			return true
		}
		if ptr, ok := results.At(i).Type().(*types.Pointer); ok && types.Identical(ptr.Elem(), targetType) {
			return true
		}
	}

	return false
}

// instructionReferencesType checks if an SSA instruction references the given type.
func instructionReferencesType(instr ssa.Instruction, targetType types.Type) bool {
	switch inst := instr.(type) {
	case *ssa.Alloc:
		return types.Identical(pointerElem(inst.Type()), targetType) || types.Identical(inst.Type(), targetType)
	case *ssa.MakeSlice:
		return types.Identical(pointerElem(inst.Type()), targetType)
	case *ssa.Store:
		if types.Identical(pointerElem(inst.Addr.Type()), targetType) {
			return true
		}
		if types.Identical(inst.Val.Type(), targetType) {
			return true
		}
	case *ssa.Call:
		// Check if any argument is our type
		for _, arg := range inst.Call.Args {
			if types.Identical(arg.Type(), targetType) || types.Identical(pointerElem(arg.Type()), targetType) {
				return true
			}
		}
	}
	return false
}

// hasOrmUsage checks if the model is used in common ORM operations.
func hasOrmUsage(funcs []*ssa.Function, targetType types.Type) bool {
	for _, fn := range funcs {
		if fn == nil {
			continue
		}

		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}

				// Check for common GORM function names
				if isOrmOperation(call) {
					// Check if first argument is our model type
					if len(call.Call.Args) > 0 {
						argType := pointerElem(call.Call.Args[0].Type())
						if types.Identical(argType, targetType) {
							return true
						}
						// Also check slice of our type
						if slice, ok := argType.(*types.Slice); ok && types.Identical(slice.Elem(), targetType) {
							return true
						}
					}

					// Check for AutoMigrate which takes variadic args
					if call.Call.Method != nil && call.Call.Method.Name() == "AutoMigrate" {
						for _, arg := range call.Call.Args {
							argType := pointerElem(arg.Type())
							if types.Identical(argType, targetType) {
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

// isOrmOperation checks if a call is to a common ORM method.
func isOrmOperation(call *ssa.Call) bool {
	if call.Call.Method == nil {
		return false
	}

	methodName := call.Call.Method.Name()
	// Common GORM methods
	gormMethods := map[string]bool{
		"Find":       true,
		"First":      true,
		"Last":       true,
		"Take":       true,
		"Create":     true,
		"Save":       true,
		"Update":     true,
		"Delete":     true,
		"Where":      true,
		"Model":      true,
		"AutoMigrate": true,
	}

	return gormMethods[methodName]
}

// extractGormModelsFromSyntax walks pkg.Syntax for every package to collect
// structs with GORM tags. Called once during loadProgram before pkg.Syntax is
// cleared, so no source re-parsing is needed.
func extractGormModelsFromSyntax(pkgs []*packages.Package) []GormModel {
	var models []GormModel
	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				genDecl, ok := n.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					return true
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					// Check if any field has a GORM tag
					if hasGormTagInAST(structType) {
						pos := pkg.Fset.Position(typeSpec.Pos())
						models = append(models, GormModel{
							Name: typeSpec.Name.Name,
							Pkg:  pkg.PkgPath,
							File: pos.Filename,
							Line: pos.Line,
						})
					}
				}
				return true
			})
		}
	}
	return models
}

// hasGormTagInAST checks if any struct field has a GORM tag in the AST.
func hasGormTagInAST(structType *ast.StructType) bool {
	if structType.Fields == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		if field.Tag != nil {
			tag := strings.Trim(field.Tag.Value, "`")
			if strings.Contains(tag, "gorm:") || strings.Contains(tag, "gorm\"") {
				return true
			}
		}
	}
	return false
}