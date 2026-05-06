package analyzer

import (
	"go/ast"
	"go/token"
	"math"
)

type halsteadMetrics struct {
	DistinctOperators int
	DistinctOperands  int
	TotalOperators    int
	TotalOperands     int
	Vocabulary        int
	Length            int
	Volume            float64
	Difficulty        float64
	Effort            float64
}

func analyzeFuncHalstead(fd *ast.FuncDecl) halsteadMetrics {
	analyzer := &halsteadAnalyzer{
		operators: make(map[string]int, 32),
		operands:  make(map[string]int, 64),
	}
	analyzer.visitBlock(fd.Body)
	return analyzer.metrics()
}

type halsteadAnalyzer struct {
	operators map[string]int
	operands  map[string]int
}

func (a *halsteadAnalyzer) addOperator(operator string) {
	if operator == "" {
		return
	}
	a.operators[operator]++
}

func (a *halsteadAnalyzer) addOperand(operand string) {
	if operand == "" || operand == "_" {
		return
	}
	a.operands[operand]++
}

func (a *halsteadAnalyzer) metrics() halsteadMetrics {
	distinctOperators := len(a.operators)
	distinctOperands := len(a.operands)
	totalOperators := totalHalsteadItems(a.operators)
	totalOperands := totalHalsteadItems(a.operands)
	vocabulary := distinctOperators + distinctOperands
	length := totalOperators + totalOperands

	volume := 0.0
	if vocabulary > 0 && length > 0 {
		volume = float64(length) * math.Log2(float64(vocabulary))
	}
	difficulty := 0.0
	if distinctOperands > 0 {
		difficulty = (float64(distinctOperators) / 2.0) * (float64(totalOperands) / float64(distinctOperands))
	}
	effort := difficulty * volume

	return halsteadMetrics{
		DistinctOperators: distinctOperators,
		DistinctOperands:  distinctOperands,
		TotalOperators:    totalOperators,
		TotalOperands:     totalOperands,
		Vocabulary:        vocabulary,
		Length:            length,
		Volume:            roundMetric(volume),
		Difficulty:        roundMetric(difficulty),
		Effort:            roundMetric(effort),
	}
}

func totalHalsteadItems(items map[string]int) int {
	total := 0
	for _, count := range items {
		total += count
	}
	return total
}

func (a *halsteadAnalyzer) visitBlock(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		a.visitStmt(stmt)
	}
}

func (a *halsteadAnalyzer) visitStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.IfStmt:
		a.addOperator("if")
		a.visitStmt(s.Init)
		a.visitExpr(s.Cond)
		a.visitBlock(s.Body)
		if s.Else != nil {
			a.addOperator("else")
			a.visitStmt(s.Else)
		}
	case *ast.ForStmt:
		a.addOperator("for")
		a.visitStmt(s.Init)
		a.visitExpr(s.Cond)
		a.visitStmt(s.Post)
		a.visitBlock(s.Body)
	case *ast.RangeStmt:
		a.addOperator("range")
		if s.Tok != token.ILLEGAL {
			a.addOperator(s.Tok.String())
		}
		a.visitExpr(s.Key)
		a.visitExpr(s.Value)
		a.visitExpr(s.X)
		a.visitBlock(s.Body)
	case *ast.SwitchStmt:
		a.addOperator("switch")
		a.visitStmt(s.Init)
		a.visitExpr(s.Tag)
		a.visitCaseClauses(s.Body)
	case *ast.TypeSwitchStmt:
		a.addOperator("type_switch")
		a.visitStmt(s.Init)
		a.visitStmt(s.Assign)
		a.visitCaseClauses(s.Body)
	case *ast.SelectStmt:
		a.addOperator("select")
		a.visitCommClauses(s.Body)
	case *ast.BlockStmt:
		a.visitBlock(s)
	case *ast.ExprStmt:
		a.visitExpr(s.X)
	case *ast.AssignStmt:
		a.addOperator(s.Tok.String())
		for _, expr := range s.Lhs {
			a.visitExpr(expr)
		}
		for _, expr := range s.Rhs {
			a.visitExpr(expr)
		}
	case *ast.ReturnStmt:
		a.addOperator("return")
		for _, expr := range s.Results {
			a.visitExpr(expr)
		}
	case *ast.DeclStmt:
		a.visitDecl(s.Decl)
	case *ast.GoStmt:
		a.addOperator("go")
		a.visitExpr(s.Call)
	case *ast.DeferStmt:
		a.addOperator("defer")
		a.visitExpr(s.Call)
	case *ast.IncDecStmt:
		a.addOperator(s.Tok.String())
		a.visitExpr(s.X)
	case *ast.LabeledStmt:
		a.addOperator("label")
		a.addOperand(s.Label.Name)
		a.visitStmt(s.Stmt)
	case *ast.BranchStmt:
		a.addOperator(s.Tok.String())
		if s.Label != nil {
			a.addOperand(s.Label.Name)
		}
	case *ast.SendStmt:
		a.addOperator("<-")
		a.visitExpr(s.Chan)
		a.visitExpr(s.Value)
	}
}

func (a *halsteadAnalyzer) visitCaseClauses(body *ast.BlockStmt) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			a.visitStmt(stmt)
			continue
		}
		if len(clause.List) == 0 {
			a.addOperator("default")
		} else {
			a.addOperator("case")
		}
		for _, expr := range clause.List {
			a.visitExpr(expr)
		}
		for _, stmt := range clause.Body {
			a.visitStmt(stmt)
		}
	}
}

func (a *halsteadAnalyzer) visitCommClauses(body *ast.BlockStmt) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CommClause)
		if !ok {
			a.visitStmt(stmt)
			continue
		}
		if clause.Comm == nil {
			a.addOperator("default")
		} else {
			a.addOperator("case")
		}
		a.visitStmt(clause.Comm)
		for _, stmt := range clause.Body {
			a.visitStmt(stmt)
		}
	}
}

func (a *halsteadAnalyzer) visitDecl(decl ast.Decl) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	a.addOperator(gen.Tok.String())
	for _, spec := range gen.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, name := range s.Names {
				a.addOperand(name.Name)
			}
			a.visitExpr(s.Type)
			for _, expr := range s.Values {
				a.visitExpr(expr)
			}
		case *ast.TypeSpec:
			a.addOperand(s.Name.Name)
			a.visitExpr(s.Type)
		}
	}
}

func (a *halsteadAnalyzer) visitExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		a.addOperand(e.Name)
	case *ast.BasicLit:
		a.addOperand(e.Value)
	case *ast.BinaryExpr:
		a.addOperator(e.Op.String())
		a.visitExpr(e.X)
		a.visitExpr(e.Y)
	case *ast.CallExpr:
		a.addOperator("call")
		a.visitExpr(e.Fun)
		for _, arg := range e.Args {
			a.visitExpr(arg)
		}
	case *ast.ParenExpr:
		a.visitExpr(e.X)
	case *ast.UnaryExpr:
		a.addOperator(e.Op.String())
		a.visitExpr(e.X)
	case *ast.SelectorExpr:
		a.addOperator(".")
		a.visitExpr(e.X)
		if e.Sel != nil {
			a.addOperand(e.Sel.Name)
		}
	case *ast.IndexExpr:
		a.addOperator("index")
		a.visitExpr(e.X)
		a.visitExpr(e.Index)
	case *ast.IndexListExpr:
		a.addOperator("index")
		a.visitExpr(e.X)
		for _, index := range e.Indices {
			a.visitExpr(index)
		}
	case *ast.SliceExpr:
		a.addOperator("slice")
		a.visitExpr(e.X)
		a.visitExpr(e.Low)
		a.visitExpr(e.High)
		a.visitExpr(e.Max)
	case *ast.StarExpr:
		a.addOperator("*")
		a.visitExpr(e.X)
	case *ast.TypeAssertExpr:
		a.addOperator("type_assert")
		a.visitExpr(e.X)
		a.visitExpr(e.Type)
	case *ast.CompositeLit:
		a.addOperator("composite_lit")
		a.visitExpr(e.Type)
		for _, elt := range e.Elts {
			a.visitExpr(elt)
		}
	case *ast.KeyValueExpr:
		a.addOperator(":")
		a.visitExpr(e.Key)
		a.visitExpr(e.Value)
	case *ast.FuncLit:
		a.addOperator("func_lit")
		a.visitExpr(e.Type)
		a.visitBlock(e.Body)
	case *ast.ArrayType:
		a.addOperator("array_type")
		a.visitExpr(e.Len)
		a.visitExpr(e.Elt)
	case *ast.MapType:
		a.addOperator("map_type")
		a.visitExpr(e.Key)
		a.visitExpr(e.Value)
	case *ast.ChanType:
		a.addOperator("chan_type")
		a.visitExpr(e.Value)
	case *ast.StructType:
		a.addOperator("struct_type")
		a.visitFieldList(e.Fields)
	case *ast.InterfaceType:
		a.addOperator("interface_type")
		a.visitFieldList(e.Methods)
	case *ast.FuncType:
		a.addOperator("func_type")
		a.visitFieldList(e.Params)
		a.visitFieldList(e.Results)
	case *ast.Ellipsis:
		a.addOperator("ellipsis")
		a.visitExpr(e.Elt)
	}
}

func (a *halsteadAnalyzer) visitFieldList(fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			a.addOperand(name.Name)
		}
		a.visitExpr(field.Type)
	}
}

func maintainabilityIndex(volume float64, cyclomatic, bodyLines int) float64 {
	if volume < 1 {
		volume = 1
	}
	if bodyLines < 1 {
		bodyLines = 1
	}
	score := (171 - 5.2*math.Log(volume) - 0.23*float64(cyclomatic) - 16.2*math.Log(float64(bodyLines))) * 100 / 171
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return roundMetric(score)
}

func roundMetric(value float64) float64 {
	return math.Round(value*100) / 100
}
