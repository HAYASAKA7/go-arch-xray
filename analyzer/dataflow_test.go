package analyzer

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestBuildFunctionAccessSummaries(t *testing.T) {
	dir := createConcurrencyTestModule(t, "summarybatched", map[string]string{
		"main.go": `package main

import (
	"sync"
	"sync/atomic"
)

type DirectState struct{ Count int }
func DirectWrite(s *DirectState) { s.Count++ }
func DirectRead(s *DirectState) int { return s.Count }

type NestedInner struct{ Count int }
type NestedState struct{ Inner NestedInner }
func NestedWrite(s *NestedState) { s.Inner.Count++ }

type PhiState struct{ Count, Other int }
func PhiAliasWrite(s *PhiState, choose bool) {
	var p *int
	if choose { p = &s.Count } else { p = &s.Count }
	*p++
}
func AmbiguousPhiAliasWrite(s *PhiState, choose bool) {
	var p *int
	if choose { p = &s.Count } else { p = &s.Other }
	*p++
}
func LocalPointerSlotAliasWrite(s *PhiState) {
	var p *int
	p = &s.Count
	*p++
}

type L5 struct{ Count int }
type L4 struct{ L5 L5 }
type L3 struct{ L4 L4 }
type L2 struct{ L3 L3 }
type L1 struct{ L2 L2 }
type DeepState struct{ L1 L1 }
func FieldPathCap(s *DeepState) { s.L1.L2.L3.L4.L5.Count++ }

type GlobalState struct{ Count int }
var shared GlobalState
func GlobalWrite() { shared.Count++ }

type ContainerState struct{ Values []int }
func ContainerMutation(s *ContainerState) { s.Values[0]++ }

type MutexState struct {
	mu sync.Mutex
	Count int
}
func MutexLocksetWrite(s *MutexState) {
	s.mu.Lock()
	s.Count++
	s.mu.Unlock()
}
func DeferUnlockWrite(s *MutexState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Count++
}

type RWState struct {
	mu sync.RWMutex
	Count int
}
func RWMutexRLocksetRead(s *RWState) int {
	s.mu.RLock()
	v := s.Count
	s.mu.RUnlock()
	return v
}

type OtherLockState struct{ mu sync.Mutex }
func UnrelatedLocksetWrite(s *MutexState, other *OtherLockState) {
	other.mu.Lock()
	s.Count++
	other.mu.Unlock()
}

type AtomicState struct{ Count int64 }
func AtomicWrite(s *AtomicState) { atomic.AddInt64(&s.Count, 1) }
`,
	})

	ws := newTestWorkspace(t)
	prog, err := ws.GetOrLoadSSA(dir, "./...")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	summaries := BuildFunctionAccessSummaries(prog)

	tests := []struct {
		name      string
		function  string
		kind      AccessKind
		rootKind  MemoryRootKind
		typeName  string
		fieldPath []string
	}{
		{"direct write", "DirectWrite", AccessWrite, RootParam, "DirectState", []string{"Count"}},
		{"direct read", "DirectRead", AccessRead, RootParam, "DirectState", []string{"Count"}},
		{"nested write", "NestedWrite", AccessWrite, RootParam, "NestedState", []string{"Inner", "Count"}},
		{"phi alias write", "PhiAliasWrite", AccessWrite, RootParam, "PhiState", []string{"Count"}},
		{"local pointer slot alias write", "LocalPointerSlotAliasWrite", AccessWrite, RootParam, "PhiState", []string{"Count"}},
		{"ambiguous phi alias unknown", "AmbiguousPhiAliasWrite", AccessWrite, RootUnknown, "PhiState", nil},
		{"global write", "GlobalWrite", AccessWrite, RootGlobal, "GlobalState", []string{"Count"}},
		{"container unknown", "ContainerMutation", AccessWrite, RootUnknown, "ContainerState", []string{"Values"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary := findAccessSummary(t, summaries, tc.function)
			if !summaryHasAccess(summary, tc.kind, tc.rootKind, tc.typeName, tc.fieldPath) {
				t.Fatalf("missing %s access in %s summary: %#v", tc.name, tc.function, summary)
			}
		})
	}

	t.Run("field path cap note", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "FieldPathCap")
		if !summaryHasNoteContaining(summary, "field path tracking capped") {
			t.Fatalf("missing field path cap note in summary: %#v", summary)
		}
	})

	t.Run("mutex lockset", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "MutexLocksetWrite")
		access, ok := findSummaryAccess(summary, AccessWrite, RootParam, "MutexState", []string{"Count"})
		if !ok {
			t.Fatalf("missing MutexState.Count write: %#v", summary)
		}
		if !locksetHasField(access.Lockset, RootParam, "MutexState", []string{"mu"}) {
			t.Fatalf("missing MutexState.mu lockset on write: %#v", access)
		}
	})

	t.Run("rwmutex rlock read lockset", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "RWMutexRLocksetRead")
		access, ok := findSummaryAccess(summary, AccessRead, RootParam, "RWState", []string{"Count"})
		if !ok {
			t.Fatalf("missing RWState.Count read: %#v", summary)
		}
		if !locksetHasField(access.Lockset, RootParam, "RWState", []string{"mu"}) {
			t.Fatalf("missing RWState.mu lockset on read: %#v", access)
		}
	})

	t.Run("defer unlock protects following access", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "DeferUnlockWrite")
		access, ok := findSummaryAccess(summary, AccessWrite, RootParam, "MutexState", []string{"Count"})
		if !ok {
			t.Fatalf("missing MutexState.Count write: %#v", summary)
		}
		if !locksetHasField(access.Lockset, RootParam, "MutexState", []string{"mu"}) {
			t.Fatalf("missing deferred MutexState.mu lockset on write: %#v", access)
		}
	})

	t.Run("unrelated lockset remains separate", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "UnrelatedLocksetWrite")
		access, ok := findSummaryAccess(summary, AccessWrite, RootParam, "MutexState", []string{"Count"})
		if !ok {
			t.Fatalf("missing MutexState.Count write: %#v", summary)
		}
		if locksetHasField(access.Lockset, RootParam, "MutexState", []string{"mu"}) {
			t.Fatalf("write should not be protected by MutexState.mu: %#v", access)
		}
		if !locksetHasField(access.Lockset, RootParam, "OtherLockState", []string{"mu"}) {
			t.Fatalf("missing unrelated OtherLockState.mu lockset on write: %#v", access)
		}
	})

	t.Run("atomic access", func(t *testing.T) {
		summary := findAccessSummary(t, summaries, "AtomicWrite")
		access, ok := findSummaryAccess(summary, AccessWrite, RootParam, "AtomicState", []string{"Count"})
		if !ok {
			t.Fatalf("missing AtomicState.Count write: %#v", summary)
		}
		if !access.Atomic {
			t.Fatalf("expected access to be marked atomic: %#v", access)
		}
	})
}

func TestExpandConcurrentAccesses(t *testing.T) {
	dir := createConcurrencyTestModule(t, "summaryexpandbatched", map[string]string{
		"main.go": `package main

type MultiArgState struct{ Count int }
func readMulti(s *MultiArgState) int { return s.Count }
func writeSecond(first, second *MultiArgState) {
	second.Count++
	_ = first
}
func RunMultiArg(first, second *MultiArgState, done chan int) {
	go func() { done <- readMulti(first) }()
	writeSecond(first, second)
}

type FieldArgInner struct{ Count int }
type FieldArgState struct {
	Left FieldArgInner
	Right FieldArgInner
}
func readField(inner *FieldArgInner) int { return inner.Count }
func incrementField(inner *FieldArgInner) { inner.Count++ }
func RunFieldArg(s *FieldArgState, done chan int) {
	go func() { done <- readField(&s.Left) }()
	incrementField(&s.Right)
}
`,
	})

	ws := newTestWorkspace(t)
	prog, err := ws.GetOrLoadSSA(dir, "./...")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	accesses := ExpandConcurrentAccesses(prog, BuildFunctionAccessSummaries(prog))

	t.Run("preserves multi-argument helper identity", func(t *testing.T) {
		var goroutineRead, parentWrite MemoryAccess
		for _, access := range accesses {
			if access.Key.TypeName != "MultiArgState" || !sameStringSlice(access.Key.FieldPath, []string{"Count"}) {
				continue
			}
			switch {
			case access.InGoroutine && access.Kind == AccessRead && access.ViaCall == "summaryexpandbatched.readMulti":
				goroutineRead = access
			case !access.InGoroutine && access.Kind == AccessWrite && access.ViaCall == "summaryexpandbatched.writeSecond":
				parentWrite = access
			}
		}
		if goroutineRead.Key.RootID == "" {
			t.Fatalf("missing goroutine read of first.Count in expanded accesses: %#v", accesses)
		}
		if parentWrite.Key.RootID == "" {
			t.Fatalf("missing parent helper write of second.Count in expanded accesses: %#v", accesses)
		}
		if sameMemoryKey(goroutineRead.Key, parentWrite.Key) {
			t.Fatalf("multi-argument helper write was attributed to goroutine read root: read=%#v write=%#v", goroutineRead, parentWrite)
		}
	})

	t.Run("composes field address argument paths", func(t *testing.T) {
		var goroutineRead, parentWrite MemoryAccess
		for _, access := range accesses {
			if access.Key.TypeName != "FieldArgState" || !sameStringSlice(access.Key.FieldPath, []string{"Left", "Count"}) && !sameStringSlice(access.Key.FieldPath, []string{"Right", "Count"}) {
				continue
			}
			switch {
			case access.InGoroutine && access.Kind == AccessRead && access.ViaCall == "summaryexpandbatched.readField":
				goroutineRead = access
			case !access.InGoroutine && access.Kind == AccessWrite && access.ViaCall == "summaryexpandbatched.incrementField":
				parentWrite = access
			}
		}
		if !sameStringSlice(goroutineRead.Key.FieldPath, []string{"Left", "Count"}) {
			t.Fatalf("expected goroutine read to target FieldArgState.Left.Count, got %#v from accesses %#v", goroutineRead, accesses)
		}
		if !sameStringSlice(parentWrite.Key.FieldPath, []string{"Right", "Count"}) {
			t.Fatalf("expected parent write to target FieldArgState.Right.Count, got %#v from accesses %#v", parentWrite, accesses)
		}
		if sameMemoryKey(goroutineRead.Key, parentWrite.Key) {
			t.Fatalf("distinct field address arguments collapsed to same key: read=%#v write=%#v", goroutineRead, parentWrite)
		}
	})
}

func TestExpandConcurrentAccessesWithNotes_ReportsExactCapOmissions(t *testing.T) {
	var funcs []*ssa.Function
	summaries := make(map[*ssa.Function]FunctionAccessSummary)
	for i := 0; i <= maxExpandedConcurrencyAccesses; i++ {
		fn := &ssa.Function{}
		funcs = append(funcs, fn)
		summaries[fn] = FunctionAccessSummary{
			Function: fn,
			GlobalAccesses: []MemoryAccess{{
				Key:  MemoryKey{RootKind: RootGlobal, RootID: "shared", TypeName: "State", FieldPath: []string{"Count"}},
				Kind: AccessRead,
			}},
		}
	}

	accesses, notes := ExpandConcurrentAccessesWithNotes(&LoadedProgram{SSAFuncs: funcs}, summaries)
	if len(accesses) != maxExpandedConcurrencyAccesses {
		t.Fatalf("expected access expansion to stop at cap %d, got %d", maxExpandedConcurrencyAccesses, len(accesses))
	}
	if len(notes) == 0 {
		t.Fatalf("expected truncation note when exact cap causes later omissions")
	}
}

func findAccessSummary(t *testing.T, summaries map[*ssa.Function]FunctionAccessSummary, shortName string) FunctionAccessSummary {
	t.Helper()
	for fn, summary := range summaries {
		if fn != nil && fn.Name() == shortName {
			return summary
		}
	}
	t.Fatalf("missing summary for function %s", shortName)
	return FunctionAccessSummary{}
}

func summaryHasAccess(summary FunctionAccessSummary, kind AccessKind, rootKind MemoryRootKind, typeName string, fieldPath []string) bool {
	_, ok := findSummaryAccess(summary, kind, rootKind, typeName, fieldPath)
	return ok
}

func findSummaryAccess(summary FunctionAccessSummary, kind AccessKind, rootKind MemoryRootKind, typeName string, fieldPath []string) (MemoryAccess, bool) {
	for _, access := range summary.AllAccesses() {
		if access.Kind != kind {
			continue
		}
		if access.Key.RootKind != rootKind {
			continue
		}
		if access.Key.TypeName != typeName {
			continue
		}
		if !sameStringSlice(access.Key.FieldPath, fieldPath) {
			continue
		}
		return access, true
	}
	return MemoryAccess{}, false
}

func locksetHasField(lockset []MemoryKey, rootKind MemoryRootKind, typeName string, fieldPath []string) bool {
	for _, key := range lockset {
		if key.RootKind == rootKind && key.TypeName == typeName && sameStringSlice(key.FieldPath, fieldPath) {
			return true
		}
	}
	return false
}

func summaryHasNoteContaining(summary FunctionAccessSummary, want string) bool {
	for _, note := range summary.Notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
