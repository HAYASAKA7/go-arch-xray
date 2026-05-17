package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

const maxOrmUsageExpansionDepth = 4

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
	Name          string              `json:"name"`
	Package       string              `json:"package"`
	File          string              `json:"file"`
	Line          int                 `json:"line"`
	Anchor        string              `json:"context_anchor,omitempty"`
	ORMFramework  string              `json:"orm_framework"`
	Reason        OrphanedModelReason `json:"reason"`
	Confidence    string              `json:"confidence,omitempty"`
	Actionability string              `json:"actionability,omitempty"`
	Evidence      []string            `json:"evidence,omitempty"`
	Notes         string              `json:"notes,omitempty"`
}

// OrphanedModelResult is returned by FindOrphanedDatabaseModels.
type OrphanedModelResult struct {
	Models       []OrphanedModel       `json:"models"`
	Total        int                   `json:"total"`
	ORMFramework string                `json:"orm_framework"`
	Scanned      int                   `json:"scanned_models"`
	ScopePattern string                `json:"scope_pattern,omitempty"`
	Summary      *OrphanedModelSummary `json:"summary,omitempty"`
	Notes        []string              `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

type OrphanedModelSummary struct {
	Total        int            `json:"total"`
	Returned     int            `json:"returned"`
	ByReason     map[string]int `json:"by_reason,omitempty"`
	ByConfidence map[string]int `json:"by_confidence,omitempty"`
}

// OrphanedModelOptions tunes the orphaned model scan.
type OrphanedModelOptions struct {
	// ORMFramework specifies which ORM to detect.
	ORMFramework string
	ScopePattern string
	// MigrationDirs specifies where to look for migration files.
	MigrationDirs []string
	// TableInference specifies how to infer table names (e.g. "snake_plural").
	TableInference string
}

// FindOrphanedDatabaseModels reports database models that are defined but never
// used in queries or migrations.
func FindOrphanedDatabaseModels(ws *Workspace, dir, pattern string, opts OrphanedModelOptions) (*OrphanedModelResult, error) {
	return FindOrphanedDatabaseModelsWithOptions(ws, dir, pattern, opts, QueryOptions{})
}

// FindOrphanedDatabaseModelsWithOptions is the streaming/paginated variant.
func FindOrphanedDatabaseModelsWithOptions(ws *Workspace, dir, pattern string, oOpts OrphanedModelOptions, qOpts QueryOptions) (*OrphanedModelResult, error) {
	prog, err := ws.GetOrLoadSSA(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}
	scopePkgs := selectedPackageSet(prog, dir, SplitPatterns(oOpts.ScopePattern))

	// Default to GORM if not specified
	framework := oOpts.ORMFramework
	if framework == "" {
		framework = "gorm"
	}

	if framework != "gorm" && framework != "ent" && framework != "sqlx" && framework != "bun" && framework != "sqlc" {
		return nil, fmt.Errorf("unsupported ORM framework: %s (currently 'gorm', 'ent', 'sqlx', 'bun', and 'sqlc' are supported)", framework)
	}

	// Use cached ORM models extracted during load
	cachedModels := prog.ormModels
	if cachedModels == nil {
		cachedModels = []OrmModel{}
	}

	// Convert cached models to a more detailed format for analysis
	type detailedModel struct {
		OrmModel
		SSAType types.Type
	}

	detailed := make([]detailedModel, 0, len(cachedModels))
	for _, om := range cachedModels {
		typ, err := findModelType(prog, om)
		if err != nil {
			continue // Skip if we can't find the type
		}
		if len(scopePkgs) > 0 && !scopePkgs[om.Pkg] {
			continue
		}
		detailed = append(detailed, detailedModel{
			OrmModel: om,
			SSAType:  typ,
		})
	}

	// Check each model for database usage
	orphaned := make([]OrphanedModel, 0, len(detailed)/4) // Assume ~25% are orphaned
	summary := &OrphanedModelSummary{
		ByReason:     make(map[string]int),
		ByConfidence: make(map[string]int),
	}

	// Read migrations if configured
	migrationFilesText := readMigrationFiles(dir, oOpts.MigrationDirs)

	for _, model := range detailed {
		refs := countStructReferences(prog.SSAFuncs, model.SSAType)
		ormUsage := hasOrmUsage(prog.SSAFuncs, model.SSAType, model.Framework)
		tableName := inferTableName(model.Name, model.Framework, oOpts.TableInference)
		inMigrations := checkInMigrations(tableName, migrationFilesText)

		var reason OrphanedModelReason
		var notes string

		switch {
		case refs == 0 && !ormUsage && !inMigrations:
			reason = OrphanedNoReferences
			notes = fmt.Sprintf("no references to this struct type found in the program and table %q not found in migrations", tableName)
		case refs > 0 && !ormUsage && !inMigrations:
			reason = OrphanedNoOrmUsage
			// No ORM usage and not in migrations
			notes = fmt.Sprintf("found %d reference(s), no ORM usage, and table %q not found in migrations", refs, tableName)
		case refs == 0 && !ormUsage && len(migrationFilesText) == 0:
			reason = OrphanedNoReferences
			notes = "no references to this struct type found in the program"
		case refs > 0 && !ormUsage && len(migrationFilesText) == 0:
			reason = OrphanedNoOrmUsage
			notes = fmt.Sprintf("found %d reference(s) but no ORM/database operations; no migration directories were configured", refs)
		default:
			continue // Model is used, not orphaned
		}
		confidence, actionability := orphanedModelMetadata(reason)

		if len(migrationFilesText) > 0 && !inMigrations && reason != OrphanedNoOrmUsage {
			notes += fmt.Sprintf(" (also not found in migrations as %q)", tableName)
		}

		orphaned = append(orphaned, OrphanedModel{
			Name:          model.Name,
			Package:       model.Pkg,
			File:          model.File,
			Line:          model.Line,
			Anchor:        contextAnchor(model.File, model.Line, model.Name),
			ORMFramework:  model.Framework,
			Reason:        reason,
			Confidence:    confidence,
			Actionability: actionability,
			Evidence: []string{
				fmt.Sprintf("references=%d", refs),
				fmt.Sprintf("orm_usage=%t", ormUsage),
				fmt.Sprintf("in_migrations=%t", inMigrations),
			},
			Notes: notes,
		})
		summary.ByReason[string(reason)]++
		summary.ByConfidence[confidence]++
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
		ScopePattern: oOpts.ScopePattern,
		Summary:      summary,
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
	summary.Total = len(orphaned)
	summary.Returned = len(result.Models)

	return result, nil
}

func orphanedModelMetadata(reason OrphanedModelReason) (confidence, actionability string) {
	switch reason {
	case OrphanedNoReferences:
		return "high", "candidate_for_delete"
	case OrphanedNoOrmUsage:
		return "medium", "verify_before_delete"
	default:
		return "unknown", "review"
	}
}

func orphanedModelKey(m OrphanedModel) string {
	return m.Package + "|" + m.Name + "|" + m.File + ":" + fmt.Sprintf("%d", m.Line)
}

// findModelType finds the types.Type for an OrmModel by looking it up in the package.
func findModelType(prog *LoadedProgram, om OrmModel) (types.Type, error) {
	for _, pkg := range prog.Packages {
		if pkg == nil || pkg.Types == nil || pkg.PkgPath != om.Pkg {
			continue
		}
		scope := pkg.Types.Scope()
		obj := scope.Lookup(om.Name)
		if obj == nil {
			continue
		}
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		return tn.Type(), nil
	}
	return nil, fmt.Errorf("model type not found: %s.%s", om.Pkg, om.Name)
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
		return typeContainsModel(inst.Type(), targetType)
	case *ssa.MakeSlice:
		return typeContainsModel(inst.Type(), targetType)
	case *ssa.Store:
		if typeContainsModel(inst.Addr.Type(), targetType) {
			return true
		}
		if typeContainsModel(inst.Val.Type(), targetType) {
			return true
		}
	case *ssa.Call:
		// Check if any argument is our type
		for _, arg := range inst.Call.Args {
			if valueContainsModel(arg, targetType) {
				return true
			}
		}
	}
	return false
}

// hasOrmUsage checks if the model is used in common ORM operations.
func hasOrmUsage(funcs []*ssa.Function, targetType types.Type, framework string) bool {
	summaries := buildOrmUsageSummaries(funcs, framework)
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
				if callUsesModelInOrm(call, targetType, framework, summaries, maxOrmUsageExpansionDepth, make(map[string]bool)) {
					return true
				}
			}
		}
	}

	return false
}

type ormUsageSummary struct {
	ParamIndexes   map[int]bool
	ReceiverDriven bool
}

func buildOrmUsageSummaries(funcs []*ssa.Function, framework string) map[*ssa.Function]ormUsageSummary {
	summaries := make(map[*ssa.Function]ormUsageSummary, len(funcs))
	for _, fn := range funcs {
		if fn != nil {
			summaries[fn] = ormUsageSummary{ParamIndexes: make(map[int]bool)}
		}
	}

	changed := true
	for pass := 0; pass < maxOrmUsageExpansionDepth && changed; pass++ {
		changed = false
		for _, fn := range funcs {
			if fn == nil {
				continue
			}
			summary := summaries[fn]
			if summary.ParamIndexes == nil {
				summary.ParamIndexes = make(map[int]bool)
			}
			for _, block := range fn.Blocks {
				for _, instr := range block.Instrs {
					call, ok := instr.(*ssa.Call)
					if !ok {
						continue
					}
					if isOrmOperation(call, framework) {
						for _, idx := range callModelParamIndexes(fn, call.Call.Args) {
							if !summary.ParamIndexes[idx] {
								summary.ParamIndexes[idx] = true
								changed = true
							}
						}
						if callReceiverComesFromFunctionParam(fn, call) && !summary.ReceiverDriven {
							summary.ReceiverDriven = true
							changed = true
						}
						continue
					}
					callee := call.Call.StaticCallee()
					if callee == nil {
						continue
					}
					calleeSummary, ok := summaries[callee]
					if !ok {
						continue
					}
					if calleeSummary.ReceiverDriven && callReceiverComesFromFunctionParam(fn, call) && !summary.ReceiverDriven {
						summary.ReceiverDriven = true
						changed = true
					}
					for idx := range forwardedOrmParamIndexes(fn, call, calleeSummary) {
						if !summary.ParamIndexes[idx] {
							summary.ParamIndexes[idx] = true
							changed = true
						}
					}
				}
			}
			summaries[fn] = summary
		}
	}
	return summaries
}

func callUsesModelInOrm(call *ssa.Call, targetType types.Type, framework string, summaries map[*ssa.Function]ormUsageSummary, depth int, seen map[string]bool) bool {
	if call == nil {
		return false
	}
	if isOrmOperation(call, framework) {
		for _, arg := range call.Call.Args {
			if valueContainsModel(arg, targetType) {
				return true
			}
		}
		if receiverContainsModel(call, targetType) {
			return true
		}
	}
	if depth <= 0 {
		return false
	}
	callee := call.Call.StaticCallee()
	if callee == nil {
		return false
	}
	key := callee.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	defer delete(seen, key)

	summary, ok := summaries[callee]
	if !ok {
		return false
	}
	for paramIndex := range summary.ParamIndexes {
		argIndex := callArgIndexForCalleeParam(call, callee, paramIndex)
		if argIndex >= 0 && argIndex < len(call.Call.Args) && valueContainsModel(call.Call.Args[argIndex], targetType) {
			return true
		}
	}
	if summary.ReceiverDriven && receiverContainsModel(call, targetType) {
		return true
	}
	return false
}

func callModelParamIndexes(fn *ssa.Function, args []ssa.Value) []int {
	seen := make(map[int]bool)
	var out []int
	for _, arg := range args {
		idx := sourceParamIndex(fn, arg)
		if idx < 0 || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, idx)
	}
	return out
}

func forwardedOrmParamIndexes(fn *ssa.Function, call *ssa.Call, calleeSummary ormUsageSummary) map[int]bool {
	out := make(map[int]bool)
	for calleeParamIndex := range calleeSummary.ParamIndexes {
		argIndex := callArgIndexForCalleeParam(call, call.Call.StaticCallee(), calleeParamIndex)
		if argIndex < 0 || argIndex >= len(call.Call.Args) {
			continue
		}
		if idx := sourceParamIndex(fn, call.Call.Args[argIndex]); idx >= 0 {
			out[idx] = true
		}
	}
	return out
}

func callReceiverComesFromFunctionParam(fn *ssa.Function, call *ssa.Call) bool {
	receiver := receiverValue(call)
	if receiver == nil {
		return false
	}
	return sourceParamIndex(fn, receiver) >= 0
}

func receiverContainsModel(call *ssa.Call, targetType types.Type) bool {
	receiver := receiverValue(call)
	return receiver != nil && valueContainsModel(receiver, targetType)
}

func receiverValue(call *ssa.Call) ssa.Value {
	if call == nil {
		return nil
	}
	if callee := call.Call.StaticCallee(); callee != nil && callee.Signature != nil && callee.Signature.Recv() != nil {
		if len(call.Call.Args) > 0 {
			return call.Call.Args[0]
		}
		return nil
	}
	if call.Call.Value == nil {
		return nil
	}
	return call.Call.Value
}

func callArgIndexForCalleeParam(call *ssa.Call, callee *ssa.Function, paramIndex int) int {
	if call == nil || callee == nil || paramIndex < 0 {
		return -1
	}
	return paramIndex
}

func sourceParamIndex(fn *ssa.Function, value ssa.Value) int {
	if fn == nil || value == nil {
		return -1
	}
	for depth := 0; value != nil && depth < 8; depth++ {
		switch v := value.(type) {
		case *ssa.Parameter:
			return paramIndexByValue(fn, v)
		case *ssa.UnOp:
			value = v.X
		case *ssa.ChangeType:
			value = v.X
		case *ssa.Convert:
			value = v.X
		case *ssa.MakeInterface:
			value = v.X
		case *ssa.Slice:
			value = v.X
		default:
			return -1
		}
	}
	return -1
}

func valueContainsModel(value ssa.Value, targetType types.Type) bool {
	if value == nil {
		return false
	}
	if typeContainsModel(value.Type(), targetType) {
		return true
	}
	for depth := 0; value != nil && depth < 8; depth++ {
		switch v := value.(type) {
		case *ssa.UnOp:
			value = v.X
		case *ssa.ChangeType:
			value = v.X
		case *ssa.Convert:
			value = v.X
		case *ssa.MakeInterface:
			value = v.X
		case *ssa.Slice:
			value = v.X
		default:
			return false
		}
		if typeContainsModel(value.Type(), targetType) {
			return true
		}
	}
	return false
}

func typeContainsModel(t types.Type, targetType types.Type) bool {
	return typeContainsModelSeen(t, targetType, make(map[types.Type]bool))
}

func typeContainsModelSeen(t types.Type, targetType types.Type, seen map[types.Type]bool) bool {
	if t == nil || targetType == nil {
		return false
	}
	if types.Identical(t, targetType) {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true

	switch tt := t.(type) {
	case *types.Pointer:
		return typeContainsModelSeen(tt.Elem(), targetType, seen)
	case *types.Slice:
		return typeContainsModelSeen(tt.Elem(), targetType, seen)
	case *types.Array:
		return typeContainsModelSeen(tt.Elem(), targetType, seen)
	case *types.Named:
		if types.Identical(tt, targetType) {
			return true
		}
		return typeContainsModelSeen(tt.Underlying(), targetType, seen)
	case *types.Alias:
		return typeContainsModelSeen(types.Unalias(tt), targetType, seen)
	}
	return false
}

// isOrmOperation checks if a call is to a common ORM method.
func isOrmOperation(call *ssa.Call, framework string) bool {
	methodName := ormCallMethodName(call)
	if methodName == "" {
		return false
	}

	// GORM methods
	if framework == "gorm" {
		gormMethods := map[string]bool{
			"Association":     true,
			"Attrs":           true,
			"Assign":          true,
			"AutoMigrate":     true,
			"Count":           true,
			"Create":          true,
			"CreateInBatches": true,
			"Delete":          true,
			"Exec":            true,
			"Find":            true,
			"FindInBatches":   true,
			"First":           true,
			"FirstOrCreate":   true,
			"FirstOrInit":     true,
			"Last":            true,
			"Model":           true,
			"Pluck":           true,
			"Raw":             true,
			"Save":            true,
			"Scan":            true,
			"Scopes":          true,
			"Take":            true,
			"Update":          true,
			"Updates":         true,
			"Where":           true,
		}
		return gormMethods[methodName]
	}

	// ent methods (ent.Client)
	if framework == "ent" {
		entMethods := map[string]bool{
			"Create":    true,
			"UpdateOne": true,
			"Update":    true,
			"Delete":    true,
			"DeleteOne": true,
			"Query":     true,
			"Get":       true,
			"First":     true,
			"Count":     true,
		}
		return entMethods[methodName]
	}

	// sqlx methods (sqlx.DB, sqlx.Tx)
	if framework == "sqlx" {
		sqlxMethods := map[string]bool{
			"Get":       true,
			"Select":    true,
			"Exec":      true,
			"NamedExec": true,
			"Query":     true,
			"QueryRow":  true,
		}
		return sqlxMethods[methodName]
	}

	// bun methods (bun.DB, bun.Tx)
	if framework == "bun" {
		bunMethods := map[string]bool{
			"NewSelect":      true,
			"NewInsert":      true,
			"NewUpdate":      true,
			"NewDelete":      true,
			"NewRaw":         true,
			"NewCreateTable": true,
			"NewDropTable":   true,
			"Exec":           true,
			"QueryRow":       true,
			"Query":          true,
			"Scan":           true,
		}
		return bunMethods[methodName]
	}

	// sqlc methods (generated Queries struct)
	if framework == "sqlc" {
		if strings.HasPrefix(methodName, "Create") ||
			strings.HasPrefix(methodName, "Get") ||
			strings.HasPrefix(methodName, "List") ||
			strings.HasPrefix(methodName, "Update") ||
			strings.HasPrefix(methodName, "Delete") ||
			strings.HasPrefix(methodName, "Count") ||
			strings.HasPrefix(methodName, "Exec") {
			return true
		}
	}

	return false
}

func ormCallMethodName(call *ssa.Call) string {
	if call == nil {
		return ""
	}
	if call.Call.Method != nil {
		return call.Call.Method.Name()
	}
	if callee := call.Call.StaticCallee(); callee != nil {
		return callee.Name()
	}
	return ""
}

// extractOrmModelsFromSyntax walks pkg.Syntax for every package to collect
// database models with ORM tags. Called once during loadProgram before pkg.Syntax is
// cleared, so no source re-parsing is needed.
func extractOrmModelsFromSyntax(pkgs []*packages.Package) []OrmModel {
	var models []OrmModel
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
					framework := detectOrmFrameworkFromAST(structType, pkg.Fset.Position(typeSpec.Pos()).Filename)
					if framework != "" {
						pos := pkg.Fset.Position(typeSpec.Pos())
						models = append(models, OrmModel{
							Name:      typeSpec.Name.Name,
							Pkg:       pkg.PkgPath,
							File:      pos.Filename,
							Line:      pos.Line,
							Framework: framework,
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

// hasEntTagInAST checks if a struct implements ent.Schema or is in an ent schema file.
func hasEntTagInAST(structType *ast.StructType, filename string) bool {
	// Check if file is in ent/schema directory
	if strings.Contains(filepath.ToSlash(filename), "ent/schema/") {
		return true
	}
	// Check for field types that suggest ent (ent.Field)
	if structType.Fields == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		if field.Type != nil {
			// Look for ent.Field types in field declarations
			if sel, ok := field.Type.(*ast.SelectorExpr); ok {
				if sel.Sel != nil && (sel.Sel.Name == "Field" || sel.Sel.Name == "schema") {
					return true
				}
			}
		}
	}
	return false
}

// hasSqlxTagInAST checks if any struct field has a sqlx db tag.
func hasSqlxTagInAST(structType *ast.StructType) bool {
	if structType.Fields == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		if field.Tag != nil {
			tag := strings.Trim(field.Tag.Value, "`")
			// sqlx uses "db:" tag for column names
			if strings.Contains(tag, "db:") || strings.Contains(tag, `db"`) {
				// Exclude gorm models which also use db: tags
				if !strings.Contains(tag, "gorm:") && !strings.Contains(tag, `gorm"`) {
					return true
				}
			}
		}
	}
	return false
}

// detectOrmFrameworkFromAST detects which ORM framework a struct belongs to.
func detectOrmFrameworkFromAST(structType *ast.StructType, filename string) string {
	if hasGormTagInAST(structType) {
		return "gorm"
	}
	if hasEntTagInAST(structType, filename) {
		return "ent"
	}
	if hasSqlxTagInAST(structType) {
		return "sqlx"
	}
	if hasBunTagInAST(structType) {
		return "bun"
	}
	if isSqlcModel(filename) {
		return "sqlc"
	}
	return ""
}

// hasBunTagInAST checks if any struct field has a bun tag.
func hasBunTagInAST(structType *ast.StructType) bool {
	if structType.Fields == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		if field.Tag != nil {
			tag := strings.Trim(field.Tag.Value, "`")
			if strings.Contains(tag, "bun:") || strings.Contains(tag, `bun"`) {
				return true
			}
		}
	}
	return false
}

// isSqlcModel checks if the file is generated by sqlc.
func isSqlcModel(filename string) bool {
	base := filepath.Base(filename)
	if base == "models.go" || strings.HasSuffix(base, ".sql.go") {
		return true
	}
	return false
}

// readMigrationFiles reads all SQL and Go files in the specified migration directories.
func readMigrationFiles(dir string, migrationDirs []string) string {
	if len(migrationDirs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, mdir := range migrationDirs {
		migPath := filepath.Join(dir, mdir)
		_ = filepath.Walk(migPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if ext == ".sql" || ext == ".go" {
				b, _ := os.ReadFile(path)
				sb.Write(b)
				sb.WriteString("\n")
			}
			return nil
		})
	}
	return sb.String()
}

// inferTableName converts a struct name to a database table name based on inference rules.
func inferTableName(modelName, framework, configInference string) string {
	if configInference == "exact" {
		return modelName
	}

	// Convert to snake_case
	var sb strings.Builder
	for i, r := range modelName {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				sb.WriteByte('_')
			}
			sb.WriteByte(byte(r + 32))
		} else {
			sb.WriteRune(r)
		}
	}
	snake := sb.String()

	if configInference == "snake" {
		return snake
	}

	// Default to snake_plural (very common for GORM, ent)
	if strings.HasSuffix(snake, "y") {
		return snake[:len(snake)-1] + "ies"
	} else if strings.HasSuffix(snake, "s") {
		return snake + "es"
	}
	return snake + "s"
}

// checkInMigrations checks if a table name is mentioned in the migration files text.
func checkInMigrations(tableName, migrationsText string) bool {
	if migrationsText == "" {
		return true // If no migrations to check, assume we don't have enough info to call it orphaned via migrations
	}

	search1 := fmt.Sprintf("TABLE %s ", tableName)
	search2 := fmt.Sprintf("TABLE \"%s\"", tableName)
	search3 := fmt.Sprintf("TABLE `%s`", tableName)
	search4 := fmt.Sprintf("TABLE '%s'", tableName)
	search5 := fmt.Sprintf("table %s ", tableName)

	return strings.Contains(migrationsText, search1) ||
		strings.Contains(migrationsText, search2) ||
		strings.Contains(migrationsText, search3) ||
		strings.Contains(migrationsText, search4) ||
		strings.Contains(migrationsText, search5) ||
		strings.Contains(migrationsText, "\""+tableName+"\"") ||
		strings.Contains(migrationsText, "`"+tableName+"`") ||
		strings.Contains(migrationsText, "'"+tableName+"'")
}
