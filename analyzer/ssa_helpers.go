package analyzer

import "golang.org/x/tools/go/ssa"

func ssaFuncPos(fn *ssa.Function) (string, int) {
	if fn == nil || fn.Prog == nil {
		return "", 0
	}
	pos := fn.Prog.Fset.Position(fn.Pos())
	return pos.Filename, pos.Line
}
