package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	complexitySortCognitive          = "cognitive"
	complexitySortCyclomatic         = "cyclomatic"
	complexitySortLines              = "lines"
	complexitySortNesting            = "nesting"
	complexitySortName               = "name"
	complexitySortPackage            = "package"
	complexitySortHalsteadVolume     = "halstead_volume"
	complexitySortHalsteadDifficulty = "halstead_difficulty"
	complexitySortHalsteadEffort     = "halstead_effort"
	complexitySortMaintainability    = "maintainability"
)

// FunctionComplexity is the per-function complexity record cached during load.
// Scores are extracted from package syntax before the workspace clears ASTs.
type FunctionComplexity struct {
	Function   string `json:"function"`
	Package    string `json:"package"`
	Receiver   string `json:"receiver,omitempty"`
	Name       string `json:"name"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Anchor     string `json:"context_anchor,omitempty"`
	Cyclomatic int    `json:"cyclomatic"`
	Cognitive  int    `json:"cognitive"`
	BodyLines  int    `json:"body_lines"`
	MaxNesting int    `json:"max_nesting"`

	HalsteadDistinctOperators int     `json:"halstead_distinct_operators"`
	HalsteadDistinctOperands  int     `json:"halstead_distinct_operands"`
	HalsteadTotalOperators    int     `json:"halstead_total_operators"`
	HalsteadTotalOperands     int     `json:"halstead_total_operands"`
	HalsteadVocabulary        int     `json:"halstead_vocabulary"`
	HalsteadLength            int     `json:"halstead_length"`
	HalsteadVolume            float64 `json:"halstead_volume"`
	HalsteadDifficulty        float64 `json:"halstead_difficulty"`
	HalsteadEffort            float64 `json:"halstead_effort"`
	MaintainabilityIndex      float64 `json:"maintainability_index"`
}

// PackageComplexity aggregates function complexity by package.
type PackageComplexity struct {
	Package           string  `json:"package"`
	FunctionCount     int     `json:"function_count"`
	TotalCyclomatic   int     `json:"total_cyclomatic"`
	TotalCognitive    int     `json:"total_cognitive"`
	AverageCyclomatic float64 `json:"average_cyclomatic"`
	AverageCognitive  float64 `json:"average_cognitive"`
	MaxCyclomatic     int     `json:"max_cyclomatic"`
	MaxCognitive      int     `json:"max_cognitive"`
	MaxFunction       string  `json:"max_function,omitempty"`

	TotalHalsteadVolume         float64 `json:"total_halstead_volume"`
	AverageHalsteadVolume       float64 `json:"average_halstead_volume"`
	MaxHalsteadVolume           float64 `json:"max_halstead_volume"`
	MaxHalsteadFunction         string  `json:"max_halstead_function,omitempty"`
	AverageMaintainabilityIndex float64 `json:"average_maintainability_index"`
	MinMaintainabilityIndex     float64 `json:"min_maintainability_index"`
	MinMaintainabilityFunction  string  `json:"min_maintainability_function,omitempty"`
}

// ComplexityResult is returned by ComputeComplexityMetrics.
type ComplexityResult struct {
	Functions               []FunctionComplexity `json:"functions"`
	Packages                []PackageComplexity  `json:"packages,omitempty"`
	Total                   int                  `json:"total"`
	MinCyclomatic           int                  `json:"min_cyclomatic,omitempty"`
	MinCognitive            int                  `json:"min_cognitive,omitempty"`
	MinHalsteadVolume       float64              `json:"min_halstead_volume,omitempty"`
	MaxMaintainabilityIndex float64              `json:"max_maintainability_index,omitempty"`
	SortBy                  string               `json:"sort_by"`
	IncludePackages         bool                 `json:"include_packages"`
	Notes                   []string             `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

// ComplexityOptions tunes complexity filtering and sorting.
type ComplexityOptions struct {
	MinCyclomatic           int
	MinCognitive            int
	MinHalsteadVolume       float64
	MaxMaintainabilityIndex float64
	SortBy                  string
	IncludePackages         bool
}

// extractComplexityFromSyntax walks package syntax and emits one complexity
// record per top-level function or method declaration with a body.
func extractComplexityFromSyntax(pkgs []*packages.Package) []FunctionComplexity {
	out := make([]FunctionComplexity, 0, 128)
	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil || fd.Name == nil {
					continue
				}
				metric := buildFunctionComplexity(pkg, fd)
				if metric == nil {
					continue
				}
				out = append(out, *metric)
			}
		}
	}
	return out
}

func buildFunctionComplexity(pkg *packages.Package, fd *ast.FuncDecl) *FunctionComplexity {
	pos := pkg.Fset.Position(fd.Pos())
	if pos.Filename == "" {
		return nil
	}

	receiver := receiverName(fd)
	qualified := pkg.PkgPath + "."
	if receiver != "" {
		qualified += receiver + "."
	}
	qualified += fd.Name.Name

	cyclomatic, cognitive, maxNesting := analyzeFuncComplexity(fd)
	halstead := analyzeFuncHalstead(fd)
	bodyLines := bodyLineSpan(pkg.Fset, fd.Body)

	return &FunctionComplexity{
		Function:                  qualified,
		Package:                   pkg.PkgPath,
		Receiver:                  receiver,
		Name:                      fd.Name.Name,
		File:                      pos.Filename,
		Line:                      pos.Line,
		Anchor:                    contextAnchor(pos.Filename, pos.Line, fd.Name.Name),
		Cyclomatic:                cyclomatic,
		Cognitive:                 cognitive,
		BodyLines:                 bodyLines,
		MaxNesting:                maxNesting,
		HalsteadDistinctOperators: halstead.DistinctOperators,
		HalsteadDistinctOperands:  halstead.DistinctOperands,
		HalsteadTotalOperators:    halstead.TotalOperators,
		HalsteadTotalOperands:     halstead.TotalOperands,
		HalsteadVocabulary:        halstead.Vocabulary,
		HalsteadLength:            halstead.Length,
		HalsteadVolume:            halstead.Volume,
		HalsteadDifficulty:        halstead.Difficulty,
		HalsteadEffort:            halstead.Effort,
		MaintainabilityIndex:      maintainabilityIndex(halstead.Volume, cyclomatic, bodyLines),
	}
}

func analyzeFuncComplexity(fd *ast.FuncDecl) (int, int, int) {
	analyzer := &complexityAnalyzer{
		functionName: fd.Name.Name,
		cyclomatic:   1,
	}
	analyzer.visitBlock(fd.Body, 0)
	return analyzer.cyclomatic, analyzer.cognitive, analyzer.maxNesting
}

type complexityAnalyzer struct {
	functionName     string
	cyclomatic       int
	cognitive        int
	maxNesting       int
	recursionCounted bool
}

func (a *complexityAnalyzer) noteNesting(nesting int) {
	if nesting > a.maxNesting {
		a.maxNesting = nesting
	}
}

func (a *complexityAnalyzer) addDecision(nesting int) {
	a.cyclomatic++
	a.cognitive += 1 + nesting
}

func (a *complexityAnalyzer) visitBlock(block *ast.BlockStmt, nesting int) {
	if block == nil {
		return
	}
	a.noteNesting(nesting)
	for _, stmt := range block.List {
		a.visitStmt(stmt, nesting)
	}
}

func (a *complexityAnalyzer) visitStmt(stmt ast.Stmt, nesting int) {
	if stmt == nil {
		return
	}
	a.noteNesting(nesting)
	switch s := stmt.(type) {
	case *ast.IfStmt:
		a.addDecision(nesting)
		a.visitStmt(s.Init, nesting)
		a.visitExpr(s.Cond, nesting)
		a.visitBlock(s.Body, nesting+1)
		if s.Else != nil {
			if elseIf, ok := s.Else.(*ast.IfStmt); ok {
				a.visitStmt(elseIf, nesting)
			} else {
				a.visitStmt(s.Else, nesting+1)
			}
		}
	case *ast.ForStmt:
		a.addDecision(nesting)
		a.visitStmt(s.Init, nesting)
		a.visitExpr(s.Cond, nesting)
		a.visitStmt(s.Post, nesting)
		a.visitBlock(s.Body, nesting+1)
	case *ast.RangeStmt:
		a.addDecision(nesting)
		a.visitExpr(s.X, nesting)
		a.visitBlock(s.Body, nesting+1)
	case *ast.SwitchStmt:
		a.cognitive += 1 + nesting
		a.visitStmt(s.Init, nesting)
		a.visitExpr(s.Tag, nesting)
		a.visitCaseClauses(s.Body, nesting+1)
	case *ast.TypeSwitchStmt:
		a.cognitive += 1 + nesting
		a.visitStmt(s.Init, nesting)
		a.visitStmt(s.Assign, nesting)
		a.visitCaseClauses(s.Body, nesting+1)
	case *ast.SelectStmt:
		a.cognitive += 1 + nesting
		a.visitCommClauses(s.Body, nesting+1)
	case *ast.BranchStmt:
		if s.Tok == token.GOTO || s.Tok == token.FALLTHROUGH || s.Label != nil {
			a.cognitive++
		}
	case *ast.BlockStmt:
		a.visitBlock(s, nesting)
	case *ast.ExprStmt:
		a.visitExpr(s.X, nesting)
	case *ast.AssignStmt:
		for _, expr := range s.Lhs {
			a.visitExpr(expr, nesting)
		}
		for _, expr := range s.Rhs {
			a.visitExpr(expr, nesting)
		}
	case *ast.ReturnStmt:
		for _, expr := range s.Results {
			a.visitExpr(expr, nesting)
		}
	case *ast.DeclStmt:
		a.visitDecl(s.Decl, nesting)
	case *ast.GoStmt:
		a.visitExpr(s.Call, nesting)
	case *ast.DeferStmt:
		a.visitExpr(s.Call, nesting)
	case *ast.IncDecStmt:
		a.visitExpr(s.X, nesting)
	case *ast.LabeledStmt:
		a.visitStmt(s.Stmt, nesting)
	case *ast.SendStmt:
		a.visitExpr(s.Chan, nesting)
		a.visitExpr(s.Value, nesting)
	case *ast.EmptyStmt:
		return
	}
}

func (a *complexityAnalyzer) visitCaseClauses(body *ast.BlockStmt, nesting int) {
	if body == nil {
		return
	}
	a.noteNesting(nesting)
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			a.visitStmt(stmt, nesting)
			continue
		}
		if len(clause.List) > 0 {
			a.cyclomatic++
		}
		for _, expr := range clause.List {
			a.visitExpr(expr, nesting)
		}
		for _, stmt := range clause.Body {
			a.visitStmt(stmt, nesting)
		}
	}
}

func (a *complexityAnalyzer) visitCommClauses(body *ast.BlockStmt, nesting int) {
	if body == nil {
		return
	}
	a.noteNesting(nesting)
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CommClause)
		if !ok {
			a.visitStmt(stmt, nesting)
			continue
		}
		if clause.Comm != nil {
			a.cyclomatic++
		}
		a.visitStmt(clause.Comm, nesting)
		for _, stmt := range clause.Body {
			a.visitStmt(stmt, nesting)
		}
	}
}

func (a *complexityAnalyzer) visitDecl(decl ast.Decl, nesting int) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range gen.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, expr := range valueSpec.Values {
			a.visitExpr(expr, nesting)
		}
	}
}

func (a *complexityAnalyzer) visitExpr(expr ast.Expr, nesting int) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.LAND || e.Op == token.LOR {
			a.cyclomatic++
			a.cognitive++
		}
		a.visitExpr(e.X, nesting)
		a.visitExpr(e.Y, nesting)
	case *ast.CallExpr:
		if !a.recursionCounted && a.isRecursiveCall(e.Fun) {
			a.cognitive++
			a.recursionCounted = true
		}
		a.visitExpr(e.Fun, nesting)
		for _, arg := range e.Args {
			a.visitExpr(arg, nesting)
		}
	case *ast.ParenExpr:
		a.visitExpr(e.X, nesting)
	case *ast.UnaryExpr:
		a.visitExpr(e.X, nesting)
	case *ast.SelectorExpr:
		a.visitExpr(e.X, nesting)
	case *ast.IndexExpr:
		a.visitExpr(e.X, nesting)
		a.visitExpr(e.Index, nesting)
	case *ast.IndexListExpr:
		a.visitExpr(e.X, nesting)
		for _, index := range e.Indices {
			a.visitExpr(index, nesting)
		}
	case *ast.SliceExpr:
		a.visitExpr(e.X, nesting)
		a.visitExpr(e.Low, nesting)
		a.visitExpr(e.High, nesting)
		a.visitExpr(e.Max, nesting)
	case *ast.StarExpr:
		a.visitExpr(e.X, nesting)
	case *ast.TypeAssertExpr:
		a.visitExpr(e.X, nesting)
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			a.visitExpr(elt, nesting)
		}
	case *ast.KeyValueExpr:
		a.visitExpr(e.Key, nesting)
		a.visitExpr(e.Value, nesting)
	case *ast.FuncLit:
		a.visitBlock(e.Body, nesting+1)
	}
}

func (a *complexityAnalyzer) isRecursiveCall(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == a.functionName
	case *ast.SelectorExpr:
		return e.Sel != nil && e.Sel.Name == a.functionName
	}
	return false
}

func bodyLineSpan(fset *token.FileSet, body *ast.BlockStmt) int {
	if fset == nil || body == nil {
		return 0
	}
	startLine := fset.Position(body.Lbrace).Line
	endLine := fset.Position(body.Rbrace).Line
	lines := endLine - startLine + 1
	if lines < 0 {
		return 0
	}
	return lines
}

// ComputeComplexityMetrics returns cached per-function complexity metrics.
func ComputeComplexityMetrics(ws *Workspace, dir, pattern string) (*ComplexityResult, error) {
	return ComputeComplexityMetricsWithOptions(ws, dir, pattern, ComplexityOptions{}, QueryOptions{})
}

// ComputeComplexityMetricsWithOptions is the streaming/paginated variant.
func ComputeComplexityMetricsWithOptions(ws *Workspace, dir, pattern string, cOpts ComplexityOptions, opts QueryOptions) (*ComplexityResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	cOpts = normalizeComplexityOptions(cOpts)
	functions := filterComplexityMetrics(prog.complexityMetrics, cOpts)
	sortComplexityMetrics(functions, cOpts.SortBy)

	result := &ComplexityResult{
		Functions:               functions,
		Total:                   len(functions),
		MinCyclomatic:           cOpts.MinCyclomatic,
		MinCognitive:            cOpts.MinCognitive,
		MinHalsteadVolume:       cOpts.MinHalsteadVolume,
		MaxMaintainabilityIndex: cOpts.MaxMaintainabilityIndex,
		SortBy:                  cOpts.SortBy,
		IncludePackages:         cOpts.IncludePackages,
		Notes: []string{
			"Use this tool before refactors, during code review, and when onboarding to identify functions that are structurally hard to reason about.",
			"Cyclomatic complexity counts independent control-flow paths; cognitive complexity applies nesting penalties to approximate human reading cost.",
			"Halstead metrics estimate expression/operator density; maintainability_index combines Halstead volume, cyclomatic complexity, and body lines into a 0-100 heuristic where lower values deserve earlier review.",
			"Complexity is a refactor and test-prioritization signal, not proof of performance, security, or correctness problems.",
			"Test files (*_test.go) are not loaded into the analysis program; test helper complexity is not reported.",
		},
	}
	if cOpts.IncludePackages {
		result.Packages = aggregatePackageComplexity(prog.complexityMetrics)
	}

	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Functions, result.TotalBeforeTruncate, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Functions, complexityStreamFingerprint(dir, pattern, cOpts), complexityFunctionKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	return result, nil
}

func normalizeComplexityOptions(opts ComplexityOptions) ComplexityOptions {
	if opts.MinCyclomatic < 0 {
		opts.MinCyclomatic = 0
	}
	if opts.MinCognitive < 0 {
		opts.MinCognitive = 0
	}
	if opts.MinHalsteadVolume < 0 {
		opts.MinHalsteadVolume = 0
	}
	if opts.MaxMaintainabilityIndex < 0 {
		opts.MaxMaintainabilityIndex = 0
	}
	opts.SortBy = strings.ToLower(strings.TrimSpace(opts.SortBy))
	switch opts.SortBy {
	case "", complexitySortCognitive:
		opts.SortBy = complexitySortCognitive
	case complexitySortCyclomatic, complexitySortLines, complexitySortNesting, complexitySortName, complexitySortPackage, complexitySortHalsteadVolume, complexitySortHalsteadDifficulty, complexitySortHalsteadEffort, complexitySortMaintainability:
		return opts
	default:
		opts.SortBy = complexitySortCognitive
	}
	return opts
}

func filterComplexityMetrics(metrics []FunctionComplexity, opts ComplexityOptions) []FunctionComplexity {
	out := make([]FunctionComplexity, 0, len(metrics))
	for _, metric := range metrics {
		if opts.MinCyclomatic > 0 && metric.Cyclomatic < opts.MinCyclomatic {
			continue
		}
		if opts.MinCognitive > 0 && metric.Cognitive < opts.MinCognitive {
			continue
		}
		if opts.MinHalsteadVolume > 0 && metric.HalsteadVolume < opts.MinHalsteadVolume {
			continue
		}
		if opts.MaxMaintainabilityIndex > 0 && metric.MaintainabilityIndex > opts.MaxMaintainabilityIndex {
			continue
		}
		out = append(out, metric)
	}
	return out
}

func sortComplexityMetrics(metrics []FunctionComplexity, sortBy string) {
	sort.Slice(metrics, func(i, j int) bool {
		left, right := metrics[i], metrics[j]
		switch sortBy {
		case complexitySortCyclomatic:
			if left.Cyclomatic != right.Cyclomatic {
				return left.Cyclomatic > right.Cyclomatic
			}
		case complexitySortLines:
			if left.BodyLines != right.BodyLines {
				return left.BodyLines > right.BodyLines
			}
		case complexitySortNesting:
			if left.MaxNesting != right.MaxNesting {
				return left.MaxNesting > right.MaxNesting
			}
		case complexitySortHalsteadVolume:
			if left.HalsteadVolume != right.HalsteadVolume {
				return left.HalsteadVolume > right.HalsteadVolume
			}
		case complexitySortHalsteadDifficulty:
			if left.HalsteadDifficulty != right.HalsteadDifficulty {
				return left.HalsteadDifficulty > right.HalsteadDifficulty
			}
		case complexitySortHalsteadEffort:
			if left.HalsteadEffort != right.HalsteadEffort {
				return left.HalsteadEffort > right.HalsteadEffort
			}
		case complexitySortMaintainability:
			if left.MaintainabilityIndex != right.MaintainabilityIndex {
				return left.MaintainabilityIndex < right.MaintainabilityIndex
			}
		case complexitySortName:
			return left.Function < right.Function
		case complexitySortPackage:
			if left.Package != right.Package {
				return left.Package < right.Package
			}
		default:
			if left.Cognitive != right.Cognitive {
				return left.Cognitive > right.Cognitive
			}
		}
		if left.Cognitive != right.Cognitive {
			return left.Cognitive > right.Cognitive
		}
		if left.Cyclomatic != right.Cyclomatic {
			return left.Cyclomatic > right.Cyclomatic
		}
		if left.BodyLines != right.BodyLines {
			return left.BodyLines > right.BodyLines
		}
		return left.Function < right.Function
	})
}

func aggregatePackageComplexity(metrics []FunctionComplexity) []PackageComplexity {
	type bucket struct {
		Package                    string
		FunctionCount              int
		TotalCyclomatic            int
		TotalCognitive             int
		MaxCyclomatic              int
		MaxCognitive               int
		MaxFunctionCC              int
		MaxFunction                string
		TotalHalsteadVolume        float64
		MaxHalsteadVolume          float64
		MaxHalsteadFunction        string
		TotalMaintainabilityIndex  float64
		MinMaintainabilityIndex    float64
		MinMaintainabilityFunction string
	}

	buckets := make(map[string]*bucket, 32)
	for _, metric := range metrics {
		b := buckets[metric.Package]
		if b == nil {
			b = &bucket{Package: metric.Package}
			buckets[metric.Package] = b
		}
		b.FunctionCount++
		b.TotalCyclomatic += metric.Cyclomatic
		b.TotalCognitive += metric.Cognitive
		b.TotalHalsteadVolume += metric.HalsteadVolume
		b.TotalMaintainabilityIndex += metric.MaintainabilityIndex
		if metric.Cyclomatic > b.MaxCyclomatic {
			b.MaxCyclomatic = metric.Cyclomatic
		}
		if metric.Cognitive > b.MaxCognitive || (metric.Cognitive == b.MaxCognitive && metric.Cyclomatic > b.MaxFunctionCC) {
			b.MaxCognitive = metric.Cognitive
			b.MaxFunctionCC = metric.Cyclomatic
			b.MaxFunction = metric.Function
		}
		if metric.HalsteadVolume > b.MaxHalsteadVolume {
			b.MaxHalsteadVolume = metric.HalsteadVolume
			b.MaxHalsteadFunction = metric.Function
		}
		if b.FunctionCount == 1 || metric.MaintainabilityIndex < b.MinMaintainabilityIndex {
			b.MinMaintainabilityIndex = metric.MaintainabilityIndex
			b.MinMaintainabilityFunction = metric.Function
		}
	}

	packages := make([]PackageComplexity, 0, len(buckets))
	for _, b := range buckets {
		avgCyclomatic := 0.0
		avgCognitive := 0.0
		avgHalsteadVolume := 0.0
		avgMaintainabilityIndex := 0.0
		if b.FunctionCount > 0 {
			avgCyclomatic = float64(b.TotalCyclomatic) / float64(b.FunctionCount)
			avgCognitive = float64(b.TotalCognitive) / float64(b.FunctionCount)
			avgHalsteadVolume = b.TotalHalsteadVolume / float64(b.FunctionCount)
			avgMaintainabilityIndex = b.TotalMaintainabilityIndex / float64(b.FunctionCount)
		}
		packages = append(packages, PackageComplexity{
			Package:                     b.Package,
			FunctionCount:               b.FunctionCount,
			TotalCyclomatic:             b.TotalCyclomatic,
			TotalCognitive:              b.TotalCognitive,
			AverageCyclomatic:           avgCyclomatic,
			AverageCognitive:            avgCognitive,
			MaxCyclomatic:               b.MaxCyclomatic,
			MaxCognitive:                b.MaxCognitive,
			MaxFunction:                 b.MaxFunction,
			TotalHalsteadVolume:         roundMetric(b.TotalHalsteadVolume),
			AverageHalsteadVolume:       roundMetric(avgHalsteadVolume),
			MaxHalsteadVolume:           b.MaxHalsteadVolume,
			MaxHalsteadFunction:         b.MaxHalsteadFunction,
			AverageMaintainabilityIndex: roundMetric(avgMaintainabilityIndex),
			MinMaintainabilityIndex:     b.MinMaintainabilityIndex,
			MinMaintainabilityFunction:  b.MinMaintainabilityFunction,
		})
	}

	sort.Slice(packages, func(i, j int) bool {
		if packages[i].TotalCognitive != packages[j].TotalCognitive {
			return packages[i].TotalCognitive > packages[j].TotalCognitive
		}
		if packages[i].MaxCognitive != packages[j].MaxCognitive {
			return packages[i].MaxCognitive > packages[j].MaxCognitive
		}
		return packages[i].Package < packages[j].Package
	})
	return packages
}

func complexityFunctionKey(f FunctionComplexity) string {
	return f.Package + "|" + f.Function + "|" + f.File + ":" + fmt.Sprintf("%d", f.Line)
}

func complexityStreamFingerprint(dir, pattern string, opts ComplexityOptions) string {
	return fmt.Sprintf("complexity:%s|%s|sort=%s|mincc=%d|mincog=%d|minhv=%.2f|maxmi=%.2f", dir, pattern, opts.SortBy, opts.MinCyclomatic, opts.MinCognitive, opts.MinHalsteadVolume, opts.MaxMaintainabilityIndex)
}
