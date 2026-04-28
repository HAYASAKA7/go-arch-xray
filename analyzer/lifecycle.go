package analyzer

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type StructLifecycleResult struct {
	Struct string         `json:"struct"`
	Hops   []LifecycleHop `json:"hops"`
}

type LifecycleHop struct {
	Kind     string `json:"kind"`
	Struct   string `json:"struct"`
	Field    string `json:"field,omitempty"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Detail   string `json:"detail,omitempty"`
	Anchor   string `json:"context_anchor,omitempty"`
}

func TraceStructLifecycle(ws *Workspace, dir, pattern, structName string) (*StructLifecycleResult, error) {
	if strings.TrimSpace(structName) == "" {
		return nil, fmt.Errorf("struct name is required")
	}

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &StructLifecycleResult{Struct: structName}
	for _, fn := range prog.SSAFuncs {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				switch inst := instr.(type) {
				case *ssa.Alloc:
					if typeMatchesStruct(pointerElem(inst.Type()), structName) {
						result.Hops = append(result.Hops, lifecycleHop(prog, fn, inst, "Instantiate", structName, "", "struct allocation"))
					}
				case *ssa.Store:
					if field, ok := fieldMutation(inst.Addr, structName); ok {
						result.Hops = append(result.Hops, lifecycleHop(prog, fn, inst, "FieldMutation", structName, field, "field mutation"))
					}
				case *ssa.MakeInterface:
					if typeMatchesStruct(pointerElem(inst.X.Type()), structName) {
						result.Hops = append(result.Hops, lifecycleHop(prog, fn, inst, "InterfaceHandoff", structName, "", "struct pointer converted to interface"))
					}
				case *ssa.Call:
					if callHasInterfaceHandoff(&inst.Call, structName) {
						result.Hops = append(result.Hops, lifecycleHop(prog, fn, inst, "InterfaceHandoff", structName, "", "struct pointer passed to interface parameter"))
					}
				}
			}
		}
	}

	sort.Slice(result.Hops, func(i, j int) bool {
		if result.Hops[i].File != result.Hops[j].File {
			return result.Hops[i].File < result.Hops[j].File
		}
		if result.Hops[i].Line != result.Hops[j].Line {
			return result.Hops[i].Line < result.Hops[j].Line
		}
		if result.Hops[i].Function != result.Hops[j].Function {
			return result.Hops[i].Function < result.Hops[j].Function
		}
		return result.Hops[i].Kind < result.Hops[j].Kind
	})

	return result, nil
}

func lifecycleHop(prog *LoadedProgram, fn *ssa.Function, instr ssa.Instruction, kind, structName, field, detail string) LifecycleHop {
	tokenPos := instr.Pos()
	if !tokenPos.IsValid() {
		if makeInterface, ok := instr.(*ssa.MakeInterface); ok {
			tokenPos = makeInterface.X.Pos()
		}
	}
	pos := prog.SSA.Fset.Position(tokenPos)
	return LifecycleHop{
		Kind:     kind,
		Struct:   structName,
		Field:    field,
		Function: fn.String(),
		File:     pos.Filename,
		Line:     pos.Line,
		Detail:   detail,
		Anchor:   contextAnchor(pos.Filename, pos.Line, shortFuncName(fn.String())),
	}
}

func fieldMutation(addr ssa.Value, structName string) (string, bool) {
	fieldAddr, ok := addr.(*ssa.FieldAddr)
	if !ok {
		return "", false
	}

	ptr, ok := fieldAddr.X.Type().Underlying().(*types.Pointer)
	if !ok {
		return "", false
	}
	st, ok := ptr.Elem().Underlying().(*types.Struct)
	if !ok || !typeMatchesStruct(ptr.Elem(), structName) || fieldAddr.Field >= st.NumFields() {
		return "", false
	}

	return st.Field(fieldAddr.Field).Name(), true
}

func callHasInterfaceHandoff(call *ssa.CallCommon, structName string) bool {
	sig := call.Signature()
	if sig == nil {
		return false
	}

	for i, arg := range call.Args {
		if !typeMatchesStruct(pointerElem(arg.Type()), structName) {
			continue
		}
		paramIndex := i
		if sig.Recv() != nil && i == 0 {
			if isInterfaceType(sig.Recv().Type()) {
				return true
			}
			continue
		}
		if sig.Recv() != nil {
			paramIndex--
		}
		if paramIndex >= 0 && paramIndex < sig.Params().Len() && isInterfaceType(sig.Params().At(paramIndex).Type()) {
			return true
		}
	}
	return false
}

func pointerElem(t types.Type) types.Type {
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		return ptr.Elem()
	}
	return nil
}

func typeMatchesStruct(t types.Type, name string) bool {
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return false
	}
	return named.Obj() != nil && named.Obj().Name() == name
}

func isInterfaceType(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}
