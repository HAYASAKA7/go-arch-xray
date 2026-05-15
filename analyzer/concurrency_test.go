package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectConcurrencyRisks_BatchedScenarios(t *testing.T) {
	dir := createConcurrencyTestModule(t, "riskbatched", map[string]string{
		"main.go": `package main

import (
	"sync"
	"sync/atomic"
)

type UnprotectedState struct{ Count int }
func RunUnprotected(s *UnprotectedState) {
	go func() { s.Count++ }()
}

type MutexProtectedState struct {
	mu sync.Mutex
	Count int
}
func RunMutexProtected(s *MutexProtectedState) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.Count++
	}()
}

type HelperMutationState struct{ Count int }
func incrementHelperMutation(s *HelperMutationState) { s.Count++ }
func RunHelperMutation(s *HelperMutationState) {
	go func() { incrementHelperMutation(s) }()
}

type AliasedClosureState struct{ Count int }
func RunAliasedClosure(s *AliasedClosureState) {
	a := s
	go func() { a.Count++ }()
}

type ReadWriteState struct{ Count int }
func RunReadWrite(s *ReadWriteState, done chan int) {
	go func() { done <- s.Count }()
	s.Count++
}

type SharedLocksetState struct {
	mu sync.Mutex
	Count int
}
func RunSharedLockset(s *SharedLocksetState) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.Count++
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Count++
}

type RLockWriteState struct {
	mu sync.RWMutex
	Count int
}
func RunRLockWrite(s *RLockWriteState, done chan int) {
	go func() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		done <- s.Count
	}()
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.Count++
}

type HelperRLockState struct {
	mu sync.RWMutex
	Count int
}
func readHelperRLock(s *HelperRLockState) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Count
}
func RunHelperRLock(s *HelperRLockState, done chan int) {
	go func() { done <- readHelperRLock(s) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Count++
}

type UnrelatedLockState struct {
	mu sync.Mutex
	Count int
}
type OtherLock struct{ mu sync.Mutex }
func RunUnrelatedLock(s *UnrelatedLockState, other *OtherLock) {
	go func() {
		other.mu.Lock()
		other.mu.Unlock()
		s.Count++
	}()
}

type AtomicState struct{ Count int64 }
func RunAtomic(s *AtomicState) {
	go func() { atomic.AddInt64(&s.Count, 1) }()
	atomic.LoadInt64(&s.Count)
}

type ContainerState struct{ Values []int }
func RunContainer(s *ContainerState) {
	go func() { s.Values[0]++ }()
}

type DistinctParamState struct{ Count int }
func RunDistinctParams(left, right *DistinctParamState, done chan int) {
	go func() { done <- left.Count }()
	right.Count++
}

type DistinctLockState struct {
	mu sync.Mutex
	Count int
}
func RunDistinctLocks(shared, leftGuard, rightGuard *DistinctLockState) {
	go func() {
		leftGuard.mu.Lock()
		defer leftGuard.mu.Unlock()
		shared.Count++
	}()
	rightGuard.mu.Lock()
	defer rightGuard.mu.Unlock()
	shared.Count++
}

type GoroutineLocalState struct{ Count int }
func RunGoroutineLocal() {
	go func() {
		s := &GoroutineLocalState{}
		s.Count++
	}()
}

type ParentHelperState struct{ Count int }
func readParentHelper(s *ParentHelperState) int { return s.Count }
func incrementParentHelper(s *ParentHelperState) { s.Count++ }
func RunParentHelper(s *ParentHelperState, done chan int) {
	go func() { done <- readParentHelper(s) }()
	incrementParentHelper(s)
}

type InterfaceUnknownState struct{ Count int }
type InterfaceUnknownMutator interface{ Touch(*InterfaceUnknownState) }
func RunInterfaceUnknown(m InterfaceUnknownMutator, s *InterfaceUnknownState) {
	go func() { m.Touch(s) }()
	s.Count++
}

type DirectInterfaceState struct{ Count int }
type DirectInterfaceMutator interface{ Touch(*DirectInterfaceState) }
func RunDirectInterface(m DirectInterfaceMutator, s *DirectInterfaceState) {
	go m.Touch(s)
	s.Count++
}

type HelperSharedLockState struct {
	mu sync.Mutex
	Count int
}
func incrementHelperSharedLock(s *HelperSharedLockState) { s.Count++ }
func RunHelperSharedLock(s *HelperSharedLockState) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		incrementHelperSharedLock(s)
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	incrementHelperSharedLock(s)
}

type FieldArgInner struct{ Count int }
type FieldArgState struct {
	Left FieldArgInner
	Right FieldArgInner
}
func readFieldArg(inner *FieldArgInner) int { return inner.Count }
func incrementFieldArg(inner *FieldArgInner) { inner.Count++ }
func RunFieldArgIdentity(s *FieldArgState, done chan int) {
	go func() { done <- readFieldArg(&s.Left) }()
	incrementFieldArg(&s.Right)
}

type ReceiverOnlyMutator interface{ Touch() }
func RunReceiverOnlyInterface(m ReceiverOnlyMutator) {
	go func() { m.Touch() }()
}
func RunDirectReceiverOnlyInterface(m ReceiverOnlyMutator) {
	go m.Touch()
}
`,
	})

	ws := newTestWorkspace(t)
	result, err := DetectConcurrencyRisks(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name       string
		level      string
		structName string
		field      string
		want       bool
	}{
		{"unprotected goroutine field mutation", "High", "UnprotectedState", "Count", true},
		{"mutex protected mutation", "High", "MutexProtectedState", "Count", false},
		{"helper mutation called by goroutine", "High", "HelperMutationState", "Count", true},
		{"aliased closure mutation", "High", "AliasedClosureState", "Count", true},
		{"goroutine read and parent write", "High", "ReadWriteState", "Count", true},
		{"shared lockset accesses", "High", "SharedLocksetState", "Count", false},
		{"write protected only by rlock", "High", "RLockWriteState", "Count", true},
		{"helper rlock against parent exclusive lock", "High", "HelperRLockState", "Count", false},
		{"mutation outside unrelated lock", "High", "UnrelatedLockState", "Count", true},
		{"atomic accesses", "High", "AtomicState", "Count", false},
		{"container lower confidence", "Medium", "ContainerState", "Values", true},
		{"distinct parameters by type and field only", "High", "DistinctParamState", "Count", false},
		{"different lock instances", "High", "DistinctLockState", "Count", true},
		{"goroutine local allocation", "High", "GoroutineLocalState", "Count", false},
		{"parent helper write against goroutine read", "High", "ParentHelperState", "Count", true},
		{"interface call in goroutine lower confidence", "Medium", "InterfaceUnknownState", "Count", true},
		{"direct interface go call lower confidence", "Medium", "DirectInterfaceState", "Count", true},
		{"helper calls protected by shared callsite lock", "High", "HelperSharedLockState", "Count", false},
		{"distinct field address args do not create state risk", "High", "FieldArgState", "Count", false},
		{"distinct field address args do not create inner risk", "High", "FieldArgInner", "Count", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasConcurrencyRisk(result, tc.level, tc.structName, tc.field)
			if got != tc.want {
				t.Fatalf("hasConcurrencyRisk(%s, %s, %s)=%v, want %v; result=%#v", tc.level, tc.structName, tc.field, got, tc.want, result)
			}
		})
	}

	t.Run("receiver-only interface call note", func(t *testing.T) {
		if !hasConcurrencyNote(result, "unresolved dynamic call") {
			t.Fatalf("expected note for unresolved receiver-only interface call: %#v", result)
		}
	})

	t.Run("direct receiver-only interface go call note", func(t *testing.T) {
		if !hasConcurrencyNote(result, "unresolved dynamic goroutine call") {
			t.Fatalf("expected note for unresolved receiver-only direct go call: %#v", result)
		}
	})
}

func hasConcurrencyRisk(r *ConcurrencyRiskResult, level, structName, field string) bool {
	for _, risk := range r.Risks {
		if risk.RiskLevel == level && risk.Struct == structName && risk.Field == field {
			return true
		}
	}
	return false
}

func hasConcurrencyNote(r *ConcurrencyRiskResult, want string) bool {
	for _, note := range r.Notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

func BenchmarkBuildFunctionAccessSummaries(b *testing.B) {
	dir := createConcurrencyTestModule(b, "benchsummarybuild", map[string]string{
		"main.go": `package main

type State struct {
	Count int
}

func read(s *State) int {
	return s.Count
}

func increment(s *State) {
	s.Count++
}

func Run(s *State, done chan int) {
	go func() {
		done <- read(s)
	}()
	increment(s)
}
`,
	})

	ws := newTestWorkspace(b)
	prog, err := ws.GetOrLoadSSA(dir, "./...")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildFunctionAccessSummaries(prog)
	}
}

func BenchmarkExpandConcurrentAccesses(b *testing.B) {
	dir := createConcurrencyTestModule(b, "benchexpandaccesses", map[string]string{
		"main.go": `package main

type State struct {
	Count int
}

func read(s *State) int {
	return s.Count
}

func increment(s *State) {
	s.Count++
}

func Run(s *State, done chan int) {
	go func() {
		done <- read(s)
	}()
	increment(s)
}
`,
	})

	ws := newTestWorkspace(b)
	prog, err := ws.GetOrLoadSSA(dir, "./...")
	if err != nil {
		b.Fatal(err)
	}
	summaries := BuildFunctionAccessSummaries(prog)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ExpandConcurrentAccesses(prog, summaries)
	}
}

func createConcurrencyTestModule(t testing.TB, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + name + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
