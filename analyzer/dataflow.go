package analyzer

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type MemoryRootKind string

const (
	RootAlloc    MemoryRootKind = "alloc"
	RootGlobal   MemoryRootKind = "global"
	RootParam    MemoryRootKind = "param"
	RootReceiver MemoryRootKind = "receiver"
	RootFreeVar  MemoryRootKind = "freevar"
	RootUnknown  MemoryRootKind = "unknown"
)

type AccessKind string

const (
	AccessRead  AccessKind = "read"
	AccessWrite AccessKind = "write"
)

const maxExpandedConcurrencyAccesses = 5000

type MemoryKey struct {
	RootKind  MemoryRootKind
	RootID    string
	TypeName  string
	FieldPath []string
}

type MemoryAccess struct {
	Key         MemoryKey
	Kind        AccessKind
	Function    string
	File        string
	Line        int
	Atomic      bool
	InGoroutine bool
	GoroutineID string
	Lockset     []MemoryKey
	ViaCall     string
	Reason      string
}

type FunctionAccessSummary struct {
	Function        *ssa.Function
	ParamEffects    map[int][]MemoryAccess
	ReceiverEffects []MemoryAccess
	GlobalAccesses  []MemoryAccess
	LocalAccesses   []MemoryAccess
	Spawned         []GoroutineSpawnSummary
	Calls           []FunctionCallSummary
	UnknownEffects  []MemoryAccess
	Notes           []string
}

type GoroutineSpawnSummary struct {
	Function string
	Callee   *ssa.Function
	Args     []MemoryKey
	Bindings map[string]MemoryKey
	ID       string
	File     string
	Line     int
}

type FunctionCallSummary struct {
	Function string
	Callee   *ssa.Function
	Args     []MemoryKey
	Lockset  []MemoryKey
	File     string
	Line     int
}

func (s FunctionAccessSummary) AllAccesses() []MemoryAccess {
	total := len(s.ReceiverEffects) + len(s.GlobalAccesses) + len(s.LocalAccesses) + len(s.UnknownEffects)
	for _, accesses := range s.ParamEffects {
		total += len(accesses)
	}
	out := make([]MemoryAccess, 0, total)
	for _, accesses := range s.ParamEffects {
		out = append(out, accesses...)
	}
	out = append(out, s.ReceiverEffects...)
	out = append(out, s.GlobalAccesses...)
	out = append(out, s.LocalAccesses...)
	out = append(out, s.UnknownEffects...)
	return out
}

func BuildFunctionAccessSummaries(prog *LoadedProgram) map[*ssa.Function]FunctionAccessSummary {
	summaries := make(map[*ssa.Function]FunctionAccessSummary, len(prog.SSAFuncs))
	for _, fn := range prog.SSAFuncs {
		if fn == nil {
			continue
		}
		summaries[fn] = buildFunctionAccessSummary(prog, fn)
	}
	return summaries
}

func ExpandConcurrentAccesses(prog *LoadedProgram, summaries map[*ssa.Function]FunctionAccessSummary) []MemoryAccess {
	accesses, _ := ExpandConcurrentAccessesWithNotes(prog, summaries)
	return accesses
}

func ExpandConcurrentAccessesWithNotes(prog *LoadedProgram, summaries map[*ssa.Function]FunctionAccessSummary) ([]MemoryAccess, []string) {
	var accesses []MemoryAccess
	var notes []string
	truncated := false
	appendAccesses := func(next []MemoryAccess) {
		if len(next) == 0 {
			return
		}
		if len(accesses) >= maxExpandedConcurrencyAccesses {
			truncated = true
			return
		}
		remaining := maxExpandedConcurrencyAccesses - len(accesses)
		if len(next) > remaining {
			accesses = append(accesses, next[:remaining]...)
			truncated = true
			return
		}
		accesses = append(accesses, next...)
	}
	for _, fn := range prog.SSAFuncs {
		if fn == nil || fn.Parent() != nil {
			continue
		}
		summary, ok := summaries[fn]
		if !ok {
			continue
		}
		parentID := "parent:" + safeFunctionString(fn)
		appendAccesses(expandFunctionAccesses(summaries, fn, rootArgsForFunction(fn), nil, 3, parentID))
		for i := range accesses {
			if accesses[i].GoroutineID == parentID {
				accesses[i].GoroutineID = ""
			}
		}
		for _, spawn := range summary.Spawned {
			if spawn.Callee == nil {
				continue
			}
			gAccesses := expandFunctionAccesses(summaries, spawn.Callee, spawn.Args, spawn.Bindings, 3, spawn.ID)
			for i := range gAccesses {
				gAccesses[i].InGoroutine = true
				gAccesses[i].GoroutineID = spawn.ID
			}
			appendAccesses(gAccesses)
		}
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("concurrency access expansion capped at %d accesses; results after the cap were omitted", maxExpandedConcurrencyAccesses))
	}
	return accesses, notes
}

func safeFunctionString(fn *ssa.Function) (name string) {
	if fn == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			name = "<unknown>"
		}
	}()
	return fn.String()
}

func expandFunctionAccesses(summaries map[*ssa.Function]FunctionAccessSummary, fn *ssa.Function, args []MemoryKey, bindings map[string]MemoryKey, depth int, goroutineID string) []MemoryAccess {
	if fn == nil || depth < 0 {
		return nil
	}
	summary, ok := summaries[fn]
	if !ok {
		return nil
	}
	var out []MemoryAccess
	for idx, effects := range summary.ParamEffects {
		if idx >= len(args) {
			continue
		}
		for _, access := range effects {
			access.Key = substituteRoot(access.Key, args[idx])
			access.Lockset = substituteLockset(fn, access.Lockset, args, bindings)
			if access.GoroutineID == "" {
				access.GoroutineID = goroutineID
			}
			out = append(out, access)
		}
	}
	for _, access := range summary.ReceiverEffects {
		if len(args) == 0 {
			continue
		}
		access.Key = substituteRoot(access.Key, args[0])
		access.Lockset = substituteLockset(fn, access.Lockset, args, bindings)
		if access.GoroutineID == "" {
			access.GoroutineID = goroutineID
		}
		out = append(out, access)
	}
	for _, access := range summary.GlobalAccesses {
		if access.GoroutineID == "" {
			access.GoroutineID = goroutineID
		}
		out = append(out, access)
	}
	for _, access := range summary.LocalAccesses {
		if access.Key.RootKind == RootFreeVar {
			if replacement, ok := bindings[access.Key.RootID]; ok {
				access.Key = substituteRoot(access.Key, replacement)
			}
		}
		access.Lockset = substituteLockset(fn, access.Lockset, args, bindings)
		if access.GoroutineID == "" {
			access.GoroutineID = goroutineID
		}
		out = append(out, access)
	}
	for _, access := range summary.UnknownEffects {
		switch access.Key.RootKind {
		case RootFreeVar:
			if replacement, ok := bindings[access.Key.RootID]; ok {
				access.Key = substituteRoot(access.Key, replacement)
			}
		case RootUnknown:
			if replacement, ok := bindings[normalizedMemoryRootID(access.Key.RootID)]; ok {
				access.Key.RootID = replacement.RootID
				if access.Key.TypeName == "" {
					access.Key.TypeName = replacement.TypeName
				}
			}
		}
		access.Lockset = substituteLockset(fn, access.Lockset, args, bindings)
		if access.GoroutineID == "" {
			access.GoroutineID = goroutineID
		}
		out = append(out, access)
	}
	for _, call := range summary.Calls {
		callLockset := substituteLockset(fn, call.Lockset, args, bindings)
		nested := expandFunctionAccesses(summaries, call.Callee, substituteArgs(fn, call.Args, args, bindings), nil, depth-1, goroutineID)
		for i := range nested {
			nested[i].ViaCall = call.Function
			nested[i].Lockset = mergeLocksets(nested[i].Lockset, callLockset)
		}
		out = append(out, nested...)
	}
	return out
}

func rootArgsForFunction(fn *ssa.Function) []MemoryKey {
	if fn == nil {
		return nil
	}
	out := make([]MemoryKey, 0, len(fn.Params))
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		out = append(out, parameterMemoryKey(fn, param))
	}
	return out
}

func substituteRoot(key, replacement MemoryKey) MemoryKey {
	origType := key.TypeName
	fieldPath := append([]string(nil), replacement.FieldPath...)
	fieldPath = append(fieldPath, key.FieldPath...)
	key.RootKind = replacement.RootKind
	key.RootID = replacement.RootID
	key.FieldPath = fieldPath
	if replacement.TypeName != "" {
		key.TypeName = replacement.TypeName
	} else {
		key.TypeName = origType
	}
	return key
}

func substituteArgs(fn *ssa.Function, callArgs []MemoryKey, args []MemoryKey, bindings map[string]MemoryKey) []MemoryKey {
	out := cloneMemoryKeys(callArgs)
	for i := range out {
		switch out[i].RootKind {
		case RootFreeVar:
			if replacement, ok := bindings[out[i].RootID]; ok {
				out[i] = substituteRoot(out[i], replacement)
			}
		case RootParam, RootReceiver:
			idx := paramIndex(fn, out[i].RootID)
			if idx >= 0 && idx < len(args) {
				out[i] = substituteRoot(out[i], args[idx])
			}
		}
	}
	return out
}

func substituteLockset(fn *ssa.Function, lockset []MemoryKey, args []MemoryKey, bindings map[string]MemoryKey) []MemoryKey {
	out := cloneMemoryKeys(lockset)
	for i := range out {
		readMode := isReadLock(out[i])
		lookupRootID := lockBaseRootID(out[i].RootID)
		switch out[i].RootKind {
		case RootParam, RootReceiver:
			idx := paramIndex(fn, lookupRootID)
			if idx >= 0 && idx < len(args) {
				out[i] = substituteRoot(out[i], args[idx])
			}
		case RootFreeVar:
			if replacement, ok := bindings[lookupRootID]; ok {
				out[i] = substituteRoot(out[i], replacement)
			}
		}
		if readMode {
			out[i].RootID = readLockRootID(lockBaseRootID(out[i].RootID))
		}
	}
	return out
}

func mergeLocksets(base []MemoryKey, extra []MemoryKey) []MemoryKey {
	out := cloneMemoryKeys(base)
	for _, key := range extra {
		seen := false
		for _, existing := range out {
			if sameMemoryKey(existing, key) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, key)
		}
	}
	return out
}

func buildFunctionAccessSummary(prog *LoadedProgram, fn *ssa.Function) FunctionAccessSummary {
	builder := accessSummaryBuilder{
		prog: prog,
		fn:   fn,
		summary: FunctionAccessSummary{
			Function:     fn,
			ParamEffects: make(map[int][]MemoryAccess),
		},
	}
	builder.scan()
	return builder.summary
}

type accessSummaryBuilder struct {
	prog    *LoadedProgram
	fn      *ssa.Function
	summary FunctionAccessSummary
	aliases map[ssa.Value]MemoryKey
	lockset []MemoryKey
	capNote bool
}

func (b *accessSummaryBuilder) scan() {
	b.aliases = make(map[ssa.Value]MemoryKey)
	for _, block := range b.fn.Blocks {
		for _, instr := range block.Instrs {
			switch inst := instr.(type) {
			case *ssa.Store:
				b.recordWrite(inst.Addr, inst)
			case *ssa.UnOp:
				if inst.Op.String() == "*" {
					b.recordRead(inst.X, inst)
				}
			case *ssa.Call:
				b.recordCall(inst)
			case *ssa.Defer:
				b.recordDefer(inst)
			case *ssa.Go:
				b.recordGoroutineSpawn(inst)
			}
		}
	}
}

const maxTrackedAliasesPerFunction = 8
const maxTrackedFieldPathDepth = 4

func (b *accessSummaryBuilder) recordWrite(addr ssa.Value, instr ssa.Instruction) {
	if store, ok := instr.(*ssa.Store); ok {
		if b.recordAliasStore(store) {
			return
		}
	}
	key, ok := b.memoryKey(addr)
	if !ok {
		return
	}
	b.addAccess(MemoryAccess{
		Key:      key,
		Kind:     AccessWrite,
		Function: b.fn.String(),
		File:     b.position(instr).Filename,
		Line:     b.position(instr).Line,
		Lockset:  cloneMemoryKeys(b.lockset),
		Reason:   "ssa store",
	})
}

func (b *accessSummaryBuilder) recordRead(addr ssa.Value, instr ssa.Instruction) {
	key, ok := b.memoryKey(addr)
	if !ok {
		return
	}
	b.addAccess(MemoryAccess{
		Key:      key,
		Kind:     AccessRead,
		Function: b.fn.String(),
		File:     b.position(instr).Filename,
		Line:     b.position(instr).Line,
		Lockset:  cloneMemoryKeys(b.lockset),
		Reason:   "ssa load",
	})
}

func (b *accessSummaryBuilder) recordCall(call *ssa.Call) {
	if b.recordAtomicCall(call) {
		return
	}
	if b.recordLockCall(call.Common(), false) {
		return
	}
	b.recordStaticCall(call)
}

func (b *accessSummaryBuilder) recordDefer(deferInstr *ssa.Defer) {
	b.recordLockCall(deferInstr.Common(), true)
}

func (b *accessSummaryBuilder) recordLockCall(common *ssa.CallCommon, deferred bool) bool {
	if common == nil {
		return false
	}
	name := lockCallName(common)
	if name != "Lock" && name != "Unlock" && name != "RLock" && name != "RUnlock" {
		return false
	}
	if len(common.Args) == 0 {
		return true
	}
	key, ok := b.memoryKey(common.Args[0])
	if !ok {
		return true
	}
	switch name {
	case "Lock":
		b.addLock(key)
	case "RLock":
		b.addReadLock(key)
	case "Unlock":
		if !deferred {
			b.removeLock(key)
		}
	case "RUnlock":
		if !deferred {
			b.removeReadLock(key)
		}
	}
	return true
}

func lockCallName(common *ssa.CallCommon) string {
	if common.Method != nil {
		return common.Method.Name()
	}
	if callee := common.StaticCallee(); callee != nil {
		name := callee.Name()
		switch name {
		case "Lock", "Unlock", "RLock", "RUnlock":
			return name
		}
		text := callee.String()
		for _, suffix := range []string{".Lock", ".Unlock", ".RLock", ".RUnlock"} {
			if strings.HasSuffix(text, suffix) {
				return strings.TrimPrefix(suffix, ".")
			}
		}
	}
	return ""
}

func (b *accessSummaryBuilder) recordAtomicCall(call *ssa.Call) bool {
	if call == nil || call.Call.StaticCallee() == nil {
		return false
	}
	if !strings.Contains(call.Call.StaticCallee().String(), "sync/atomic.") {
		return false
	}
	if len(call.Call.Args) == 0 {
		return true
	}
	key, ok := b.memoryKey(call.Call.Args[0])
	if !ok {
		return true
	}
	pos := b.position(call)
	b.addAccess(MemoryAccess{
		Key:      key,
		Kind:     atomicAccessKind(call.Call.StaticCallee().Name()),
		Function: b.fn.String(),
		File:     pos.Filename,
		Line:     pos.Line,
		Atomic:   true,
		Lockset:  cloneMemoryKeys(b.lockset),
		Reason:   "sync/atomic call",
	})
	return true
}

func (b *accessSummaryBuilder) recordGoroutineSpawn(g *ssa.Go) {
	pos := b.position(g)
	callee, bindings := b.callTarget(g.Common())
	name := g.Common().String()
	if callee != nil {
		name = callee.String()
	} else {
		b.recordUnknownGoroutineEffects(g, name, pos)
	}
	b.summary.Spawned = append(b.summary.Spawned, GoroutineSpawnSummary{
		Function: name,
		Callee:   callee,
		Args:     b.argumentKeys(g.Common().Args),
		Bindings: bindings,
		ID:       fmt.Sprintf("%s:%d->%s", b.fn.String(), pos.Line, name),
		File:     pos.Filename,
		Line:     pos.Line,
	})
}

func (b *accessSummaryBuilder) recordUnknownGoroutineEffects(g *ssa.Go, name string, pos position) {
	if g == nil || g.Common() == nil {
		return
	}
	id := fmt.Sprintf("%s:%d->%s", b.fn.String(), pos.Line, name)
	added := false
	for _, arg := range g.Common().Args {
		key, ok := b.argumentKey(arg)
		if !ok || key.TypeName == "" {
			continue
		}
		key.RootKind = RootUnknown
		b.summary.UnknownEffects = append(b.summary.UnknownEffects, MemoryAccess{
			Key:         key,
			Kind:        AccessWrite,
			Function:    b.fn.String(),
			File:        pos.Filename,
			Line:        pos.Line,
			InGoroutine: true,
			GoroutineID: id,
			Lockset:     cloneMemoryKeys(b.lockset),
			Reason:      "unresolved dynamic goroutine call",
		})
		added = true
	}
	if added {
		b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("unresolved dynamic goroutine call in %s collapsed shared struct arguments to unknown effects", b.fn.String()))
	} else if g.Common().Value != nil {
		b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("unresolved dynamic goroutine call in %s has no struct arguments; receiver effects are unknown", b.fn.String()))
	}
}

func (b *accessSummaryBuilder) recordStaticCall(call *ssa.Call) {
	callee, _ := b.callTarget(call.Common())
	if callee == nil {
		b.recordUnknownCallEffects(call)
		return
	}
	pos := b.position(call)
	b.summary.Calls = append(b.summary.Calls, FunctionCallSummary{
		Function: callee.String(),
		Callee:   callee,
		Args:     b.argumentKeys(call.Call.Args),
		Lockset:  cloneMemoryKeys(b.lockset),
		File:     pos.Filename,
		Line:     pos.Line,
	})
}

func (b *accessSummaryBuilder) recordUnknownCallEffects(call *ssa.Call) {
	if call == nil || call.Common() == nil {
		return
	}
	pos := b.position(call)
	added := false
	for _, arg := range call.Common().Args {
		key, ok := b.argumentKey(arg)
		if !ok || key.TypeName == "" {
			continue
		}
		key.RootKind = RootUnknown
		b.summary.UnknownEffects = append(b.summary.UnknownEffects, MemoryAccess{
			Key:      key,
			Kind:     AccessWrite,
			Function: b.fn.String(),
			File:     pos.Filename,
			Line:     pos.Line,
			Lockset:  cloneMemoryKeys(b.lockset),
			Reason:   "unresolved dynamic call",
		})
		added = true
	}
	if added {
		b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("unresolved dynamic call in %s collapsed shared struct arguments to unknown effects", b.fn.String()))
	} else if call.Common().Value != nil {
		b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("unresolved dynamic call in %s has no struct arguments; receiver effects are unknown", b.fn.String()))
	}
}

func (b *accessSummaryBuilder) callTarget(common *ssa.CallCommon) (*ssa.Function, map[string]MemoryKey) {
	if common == nil {
		return nil, nil
	}
	closure, ok := common.Value.(*ssa.MakeClosure)
	if !ok {
		if callee := common.StaticCallee(); callee != nil {
			return callee, nil
		}
		return nil, nil
	}
	fn, ok := closure.Fn.(*ssa.Function)
	if !ok {
		return nil, nil
	}
	bindings := make(map[string]MemoryKey, len(closure.Bindings))
	for i, binding := range closure.Bindings {
		if i >= len(fn.FreeVars) {
			break
		}
		if key, ok := b.argumentKey(binding); ok {
			bindings[fn.FreeVars[i].Name()] = key
		}
	}
	for _, freevar := range fn.FreeVars {
		if freevar == nil {
			continue
		}
		name := freevar.Name()
		if _, ok := bindings[name]; ok {
			continue
		}
		if key, ok := b.paramKeyByName(name); ok {
			bindings[name] = key
		}
	}
	return fn, bindings
}

func (b *accessSummaryBuilder) paramKeyByName(name string) (MemoryKey, bool) {
	for _, param := range b.fn.Params {
		if param != nil && param.Name() == name {
			return parameterMemoryKey(b.fn, param), true
		}
	}
	return MemoryKey{}, false
}

func (b *accessSummaryBuilder) argumentKeys(args []ssa.Value) []MemoryKey {
	out := make([]MemoryKey, 0, len(args))
	for _, arg := range args {
		key, ok := b.argumentKey(arg)
		if !ok {
			key = MemoryKey{RootKind: RootUnknown, RootID: arg.Name(), TypeName: namedStructTypeName(pointerElem(arg.Type()))}
		}
		out = append(out, key)
	}
	return out
}

func (b *accessSummaryBuilder) argumentKey(v ssa.Value) (MemoryKey, bool) {
	if key, ok := b.memoryKey(v); ok {
		return key, true
	}
	if key, ok := b.aliasMemoryKey(v); ok {
		return key, true
	}
	key := b.rootKey(v)
	return key, key.RootKind != ""
}

func (b *accessSummaryBuilder) addAccess(access MemoryAccess) {
	switch access.Key.RootKind {
	case RootParam:
		idx := paramIndex(b.fn, access.Key.RootID)
		if idx >= 0 {
			b.summary.ParamEffects[idx] = append(b.summary.ParamEffects[idx], access)
			return
		}
	case RootReceiver:
		b.summary.ReceiverEffects = append(b.summary.ReceiverEffects, access)
		return
	case RootGlobal:
		b.summary.GlobalAccesses = append(b.summary.GlobalAccesses, access)
		return
	case RootUnknown:
		b.summary.UnknownEffects = append(b.summary.UnknownEffects, access)
		return
	}
	b.summary.LocalAccesses = append(b.summary.LocalAccesses, access)
}

func (b *accessSummaryBuilder) addLock(key MemoryKey) {
	for _, existing := range b.lockset {
		if sameMemoryKey(existing, key) {
			return
		}
	}
	b.lockset = append(b.lockset, key)
}

func (b *accessSummaryBuilder) addReadLock(key MemoryKey) {
	key.RootID = readLockRootID(key.RootID)
	for _, existing := range b.lockset {
		if sameMemoryKey(existing, key) {
			return
		}
	}
	b.lockset = append(b.lockset, key)
}

func (b *accessSummaryBuilder) removeLock(key MemoryKey) {
	for i, existing := range b.lockset {
		if sameMemoryKey(existing, key) {
			b.lockset = append(b.lockset[:i], b.lockset[i+1:]...)
			return
		}
	}
}

func (b *accessSummaryBuilder) removeReadLock(key MemoryKey) {
	key.RootID = readLockRootID(key.RootID)
	b.removeLock(key)
}

func (b *accessSummaryBuilder) memoryKey(addr ssa.Value) (MemoryKey, bool) {
	if index, ok := addr.(*ssa.IndexAddr); ok {
		return b.containerElementKey(index.X)
	}
	if alias, ok := b.aliasMemoryKey(addr); ok {
		return alias, true
	}

	base, fields, ok := peelFieldPath(addr)
	if !ok || len(fields) == 0 {
		return MemoryKey{}, false
	}
	root, ok := b.aliasMemoryKey(base)
	if !ok {
		root = b.rootKey(base)
	}
	if root.RootKind == "" {
		return MemoryKey{}, false
	}
	root.FieldPath = append(root.FieldPath, fields...)
	if len(root.FieldPath) > maxTrackedFieldPathDepth {
		if !b.capNote {
			b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("field path tracking capped at %d fields in %s; deeper accesses may be lower precision", maxTrackedFieldPathDepth, b.fn.String()))
			b.capNote = true
		}
		root.RootKind = RootUnknown
		root.RootID = "field-depth:" + root.RootID
		root.FieldPath = append([]string(nil), root.FieldPath[:maxTrackedFieldPathDepth]...)
	}
	if isContainerFieldPath(base.Type(), fields) {
		root.RootKind = RootUnknown
		root.RootID = "container:" + root.RootID
	}
	return root, true
}

func atomicAccessKind(name string) AccessKind {
	if strings.HasPrefix(name, "Load") {
		return AccessRead
	}
	return AccessWrite
}

func cloneMemoryKeys(keys []MemoryKey) []MemoryKey {
	if len(keys) == 0 {
		return nil
	}
	out := make([]MemoryKey, len(keys))
	copy(out, keys)
	return out
}

func (b *accessSummaryBuilder) aliasMemoryKey(v ssa.Value) (MemoryKey, bool) {
	if key, ok := b.aliases[v]; ok {
		return key, true
	}
	switch val := v.(type) {
	case *ssa.Phi:
		var out MemoryKey
		for i, edge := range val.Edges {
			key, ok := b.memoryKey(edge)
			if !ok {
				return MemoryKey{}, false
			}
			if i == 0 {
				out = key
				continue
			}
			if !sameMemoryKey(out, key) {
				return b.unknownAliasKey(out), true
			}
		}
		return out, len(val.Edges) > 0
	case *ssa.UnOp:
		if val.Op.String() == "*" {
			if key, ok := b.aliases[val.X]; ok {
				return key, true
			}
		}
	}
	return MemoryKey{}, false
}

func (b *accessSummaryBuilder) recordAliasStore(store *ssa.Store) bool {
	if pointerType(store.Val.Type()) == nil {
		return false
	}
	if _, ok := store.Addr.(*ssa.Alloc); !ok {
		return false
	}
	key, ok := b.memoryKey(store.Val)
	if !ok {
		key, ok = b.argumentKey(store.Val)
	}
	if !ok {
		return false
	}
	if len(b.aliases) >= maxTrackedAliasesPerFunction {
		if !b.capNote {
			b.summary.Notes = append(b.summary.Notes, fmt.Sprintf("alias tracking capped at %d values in %s; some accesses may be lower precision", maxTrackedAliasesPerFunction, b.fn.String()))
			b.capNote = true
		}
		return false
	}
	b.aliases[store.Addr] = key
	return true
}

func (b *accessSummaryBuilder) unknownAliasKey(example MemoryKey) MemoryKey {
	return MemoryKey{
		RootKind: RootUnknown,
		RootID:   "ambiguous:" + example.RootID,
		TypeName: example.TypeName,
	}
}

func (b *accessSummaryBuilder) containerElementKey(v ssa.Value) (MemoryKey, bool) {
	if load, ok := v.(*ssa.UnOp); ok && load.Op.String() == "*" {
		key, ok := b.memoryKey(load.X)
		if !ok {
			return MemoryKey{}, false
		}
		key.RootKind = RootUnknown
		if !strings.HasPrefix(key.RootID, "container:") {
			key.RootID = "container:" + key.RootID
		}
		return key, true
	}
	key := b.rootKey(v)
	if key.RootKind == "" {
		return MemoryKey{}, false
	}
	key.RootKind = RootUnknown
	if !strings.HasPrefix(key.RootID, "container:") {
		key.RootID = "container:" + key.RootID
	}
	return key, true
}

func (b *accessSummaryBuilder) rootKey(v ssa.Value) MemoryKey {
	switch val := v.(type) {
	case *ssa.Parameter:
		return parameterMemoryKey(b.fn, val)
	case *ssa.UnOp:
		if val.Op.String() == "*" {
			return b.rootKey(val.X)
		}
	case *ssa.Alloc:
		return MemoryKey{RootKind: RootAlloc, RootID: b.fn.String() + "|alloc|" + val.Name(), TypeName: namedStructTypeName(pointerElem(val.Type()))}
	case *ssa.Global:
		return MemoryKey{RootKind: RootGlobal, RootID: val.Pkg.Pkg.Path() + "." + val.Name(), TypeName: namedStructTypeName(pointerElem(val.Type()))}
	case *ssa.FreeVar:
		if key, ok := b.aliases[val]; ok {
			return key
		}
		return MemoryKey{RootKind: RootFreeVar, RootID: val.Name(), TypeName: namedStructTypeName(pointerElem(val.Type()))}
	}
	return MemoryKey{RootKind: RootUnknown, RootID: v.Name(), TypeName: namedStructTypeName(pointerElem(v.Type()))}
}

func parameterMemoryKey(fn *ssa.Function, p *ssa.Parameter) MemoryKey {
	rootKind := RootParam
	idx := paramIndexByValue(fn, p)
	rootID := paramRootID(fn, idx, p.Name())
	if fn.Signature != nil && fn.Signature.Recv() != nil && idx == 0 {
		rootKind = RootReceiver
	}
	return MemoryKey{RootKind: rootKind, RootID: rootID, TypeName: namedStructTypeName(pointerElem(p.Type()))}
}

func paramRootID(fn *ssa.Function, idx int, name string) string {
	if fn == nil {
		return name
	}
	return fmt.Sprintf("%s|param|%d|%s", fn.String(), idx, name)
}

func paramIndexByValue(fn *ssa.Function, p *ssa.Parameter) int {
	if fn == nil {
		return -1
	}
	for i, param := range fn.Params {
		if param == p {
			return i
		}
	}
	return -1
}

func paramIndex(fn *ssa.Function, rootID string) int {
	for i, param := range fn.Params {
		if param == nil {
			continue
		}
		if paramRootID(fn, i, param.Name()) == rootID || param.Name() == rootID {
			return i
		}
	}
	return -1
}

func peelFieldPath(v ssa.Value) (ssa.Value, []string, bool) {
	field, ok := v.(*ssa.FieldAddr)
	if !ok {
		return nil, nil, false
	}
	base, fields, ok := peelFieldPath(field.X)
	if !ok {
		base = field.X
	}
	name, ok := fieldName(field)
	if !ok {
		return nil, nil, false
	}
	fields = append(fields, name)
	return base, fields, true
}

func fieldName(field *ssa.FieldAddr) (string, bool) {
	ptr := pointerType(field.X.Type())
	if ptr == nil {
		return "", false
	}
	st, ok := ptr.Elem().Underlying().(*types.Struct)
	if !ok || field.Field >= st.NumFields() {
		return "", false
	}
	return st.Field(field.Field).Name(), true
}

func pointerType(t types.Type) *types.Pointer {
	ptr, _ := t.Underlying().(*types.Pointer)
	return ptr
}

func isContainerFieldPath(baseType types.Type, fields []string) bool {
	t := pointerElem(baseType)
	for _, fieldName := range fields {
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return true
		}
		var next types.Type
		for i := 0; i < st.NumFields(); i++ {
			if st.Field(i).Name() == fieldName {
				next = st.Field(i).Type()
				break
			}
		}
		if next == nil {
			return true
		}
		if isContainerType(next) {
			return true
		}
		t = next
	}
	return false
}

func isContainerType(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Slice, *types.Array, *types.Map, *types.Chan, *types.Interface:
		return true
	}
	return strings.Contains(t.String(), "unsafe.Pointer")
}

func namedStructTypeName(t types.Type) string {
	for {
		ptr, ok := t.(*types.Pointer)
		if !ok {
			break
		}
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return ""
	}
	if named.Obj() == nil {
		return ""
	}
	return named.Obj().Name()
}

func (b *accessSummaryBuilder) position(instr ssa.Instruction) position {
	pos := b.prog.SSA.Fset.Position(instr.Pos())
	return position{Filename: pos.Filename, Line: pos.Line}
}

type position struct {
	Filename string
	Line     int
}

func (k MemoryKey) String() string {
	return fmt.Sprintf("%s:%s.%s", k.RootKind, k.TypeName, strings.Join(k.FieldPath, "."))
}

func sameMemoryKey(a, b MemoryKey) bool {
	if a.RootKind != b.RootKind || a.RootID != b.RootID || a.TypeName != b.TypeName {
		return false
	}
	if len(a.FieldPath) != len(b.FieldPath) {
		return false
	}
	for i := range a.FieldPath {
		if a.FieldPath[i] != b.FieldPath[i] {
			return false
		}
	}
	return true
}
