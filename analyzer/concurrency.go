package analyzer

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type ConcurrencyRiskResult struct {
	Risks []ConcurrencyRisk `json:"risks"`
}

type ConcurrencyRisk struct {
	RiskLevel string `json:"risk_level"`
	Struct    string `json:"struct"`
	Field     string `json:"field,omitempty"`
	Function  string `json:"function"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Reasoning string `json:"reasoning"`
	Anchor    string `json:"context_anchor,omitempty"`
}

func DetectConcurrencyRisks(ws *Workspace, dir, pattern string) (*ConcurrencyRiskResult, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "./..."
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &ConcurrencyRiskResult{}
	for _, fn := range prog.SSAFuncs {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				goInstr, ok := instr.(*ssa.Go)
				if !ok {
					continue
				}
				target := goInstr.Common().StaticCallee()
				if target == nil {
					continue
				}
				if functionHasVisibleProtection(target) {
					continue
				}
				for _, mutation := range functionFieldMutations(prog, target) {
					mutation.Reasoning = fmt.Sprintf("Field %s.%s is modified inside goroutine %s without visible mutex Lock/Unlock or sync/atomic protection in the SSA path.", mutation.Struct, mutation.Field, target.String())
					result.Risks = append(result.Risks, mutation)
				}
			}
		}
	}

	sort.Slice(result.Risks, func(i, j int) bool {
		if result.Risks[i].File != result.Risks[j].File {
			return result.Risks[i].File < result.Risks[j].File
		}
		if result.Risks[i].Line != result.Risks[j].Line {
			return result.Risks[i].Line < result.Risks[j].Line
		}
		if result.Risks[i].Struct != result.Risks[j].Struct {
			return result.Risks[i].Struct < result.Risks[j].Struct
		}
		return result.Risks[i].Field < result.Risks[j].Field
	})

	return result, nil
}

func functionFieldMutations(prog *LoadedProgram, fn *ssa.Function) []ConcurrencyRisk {
	var risks []ConcurrencyRisk
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			store, ok := instr.(*ssa.Store)
			if !ok {
				continue
			}
			field, ok := store.Addr.(*ssa.FieldAddr)
			if !ok {
				continue
			}
			structName, fieldName, ok := fieldStructAndName(field)
			if !ok {
				continue
			}
			pos := prog.SSA.Fset.Position(store.Pos())
			risks = append(risks, ConcurrencyRisk{
				RiskLevel: "High",
				Struct:    structName,
				Field:     fieldName,
				Function:  fn.String(),
				File:      pos.Filename,
				Line:      pos.Line,
				Anchor:    contextAnchor(pos.Filename, pos.Line, shortFuncName(fn.String())),
			})
		}
	}
	return risks
}

func fieldStructAndName(fieldAddr *ssa.FieldAddr) (string, string, bool) {
	ptr := pointerType(fieldAddr.X.Type())
	if ptr == nil {
		return "", "", false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return "", "", false
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok || fieldAddr.Field >= st.NumFields() {
		return "", "", false
	}
	return named.Obj().Name(), st.Field(fieldAddr.Field).Name(), true
}

func functionHasVisibleProtection(fn *ssa.Function) bool {
	hasLock := false
	hasUnlock := false
	hasAtomic := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			common := call.Common()
			if common == nil {
				continue
			}
			if callee := common.StaticCallee(); callee != nil {
				name := callee.String()
				if strings.Contains(name, "sync/atomic.") {
					hasAtomic = true
				}
				if strings.HasSuffix(name, ".Lock") {
					hasLock = true
				}
				if strings.HasSuffix(name, ".Unlock") {
					hasUnlock = true
				}
			}
			if common.Method != nil {
				name := common.Method.Name()
				if name == "Lock" {
					hasLock = true
				}
				if name == "Unlock" {
					hasUnlock = true
				}
			}
		}
	}
	return hasAtomic || (hasLock && hasUnlock)
}

func pointerType(t types.Type) *types.Pointer {
	ptr, _ := t.Underlying().(*types.Pointer)
	return ptr
}
