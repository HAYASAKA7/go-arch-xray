package analyzer

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type StructLifecycleResult struct {
	Struct              string            `json:"struct"`
	DedupeMode          string            `json:"dedupe_mode,omitempty"`
	MaxHops             int               `json:"max_hops"`
	TotalBeforeTruncate int               `json:"total_before_truncate,omitempty"`
	Truncated           bool              `json:"truncated,omitempty"`
	Summary             *LifecycleSummary `json:"summary,omitempty"`
	Hops                []LifecycleHop    `json:"hops"`
}

type LifecycleSummary struct {
	TotalByKind     map[string]int `json:"total_by_kind"`
	TotalByFunction map[string]int `json:"total_by_function"`
	TotalByField    map[string]int `json:"total_by_field,omitempty"`
}

type LifecycleOptions struct {
	DedupeMode string
	MaxHops    int
	Summary    bool
	Limit      int
	Offset     int
	MaxItems   int
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

func TraceStructLifecycle(ws *Workspace, dir, pattern, structName string, opts LifecycleOptions) (*StructLifecycleResult, error) {
	if strings.TrimSpace(structName) == "" {
		return nil, fmt.Errorf("struct name is required")
	}
	opts = normalizeLifecycleOptions(opts)

	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &StructLifecycleResult{Struct: structName, DedupeMode: opts.DedupeMode, MaxHops: opts.MaxHops}
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

	result.Hops = dedupeLifecycleHops(result.Hops, opts.DedupeMode)
	if opts.Summary {
		result.Summary = summarizeLifecycleHops(result.Hops)
	}

	totalBeforeAnyCut := len(result.Hops)
	result.TotalBeforeTruncate = totalBeforeAnyCut
	if len(result.Hops) > opts.MaxHops {
		result.Hops = result.Hops[:opts.MaxHops]
		result.Truncated = true
	}

	window, _, truncated := applyQueryWindow(result.Hops, QueryOptions{Limit: opts.Limit, Offset: opts.Offset, MaxItems: opts.MaxItems, Summary: opts.Summary})
	result.Truncated = result.Truncated || truncated
	result.Hops = window

	return result, nil
}

func normalizeLifecycleOptions(opts LifecycleOptions) LifecycleOptions {
	mode := strings.TrimSpace(opts.DedupeMode)
	switch mode {
	case "", "none":
		mode = "none"
	case "function_field", "function_kind_field":
		// valid
	default:
		mode = "function_kind_field"
	}
	maxHops := opts.MaxHops
	if maxHops <= 0 {
		maxHops = 500
	}
	if maxHops > 20000 {
		maxHops = 20000
	}
	qo := normalizeQueryOptions(QueryOptions{Limit: opts.Limit, Offset: opts.Offset, MaxItems: opts.MaxItems, Summary: opts.Summary})
	return LifecycleOptions{DedupeMode: mode, MaxHops: maxHops, Summary: opts.Summary, Limit: qo.Limit, Offset: qo.Offset, MaxItems: qo.MaxItems}
}

func dedupeLifecycleHops(hops []LifecycleHop, mode string) []LifecycleHop {
	if mode == "none" {
		return hops
	}
	out := make([]LifecycleHop, 0, len(hops))
	seen := make(map[string]bool, len(hops))
	for _, hop := range hops {
		var key string
		switch mode {
		case "function_field":
			key = hop.Function + "|" + hop.Field
		default:
			key = hop.Function + "|" + hop.Kind + "|" + hop.Field
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hop)
	}
	return out
}

func summarizeLifecycleHops(hops []LifecycleHop) *LifecycleSummary {
	summary := &LifecycleSummary{
		TotalByKind:     make(map[string]int),
		TotalByFunction: make(map[string]int),
		TotalByField:    make(map[string]int),
	}
	for _, hop := range hops {
		summary.TotalByKind[hop.Kind]++
		summary.TotalByFunction[hop.Function]++
		if hop.Field != "" {
			summary.TotalByField[hop.Field]++
		}
	}
	if len(summary.TotalByField) == 0 {
		summary.TotalByField = nil
	}
	return summary
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
