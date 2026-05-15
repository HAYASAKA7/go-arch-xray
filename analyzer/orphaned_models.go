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
		case refs == 0 && !ormUsage:
			reason = OrphanedNoReferences
			notes = "no references to this struct type found in the program"
		case refs > 0 && !ormUsage:
			reason = OrphanedNoOrmUsage
			notes = fmt.Sprintf("found %d reference(s) but no ORM/database operations", refs)
		case !inMigrations && len(migrationFilesText) > 0 && !ormUsage:
			// No ORM usage and not in migrations
			reason = OrphanedNoOrmUsage
			notes = fmt.Sprintf("found %d reference(s), no ORM usage, and table %q not found in migrations", refs, tableName)
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
func hasOrmUsage(funcs []*ssa.Function, targetType types.Type, framework string) bool {
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

				// Check for common ORM method names
				if isOrmOperation(call, framework) {
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
func isOrmOperation(call *ssa.Call, framework string) bool {
	if call.Call.Method == nil {
		return false
	}

	methodName := call.Call.Method.Name()

	// GORM methods
	if framework == "gorm" {
		gormMethods := map[string]bool{
			"Find":        true,
			"First":       true,
			"Last":        true,
			"Take":        true,
			"Create":      true,
			"Save":        true,
			"Update":      true,
			"Delete":      true,
			"Where":       true,
			"Model":       true,
			"AutoMigrate": true,
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
