package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

type ConcurrencyRiskResult struct {
	Risks   []ConcurrencyRisk       `json:"risks"`
	Summary *ConcurrencyRiskSummary `json:"summary,omitempty"`
	Notes   []string                `json:"notes,omitempty"`
}

type ConcurrencyRiskOptions struct {
	IncludeDiagnostics bool
}

type ConcurrencyRiskSummary struct {
	TotalRisks             int `json:"total_risks"`
	UnresolvedDynamicCalls int `json:"unresolved_dynamic_calls"`
	UnknownEffects         int `json:"unknown_effects"`
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

func DetectConcurrencyRisks(ws *Workspace, dir, pattern string, opts ConcurrencyRiskOptions) (*ConcurrencyRiskResult, error) {
	prog, err := ws.GetOrLoadSSA(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	summaries := prog.ConcurrencySummaries()
	accesses, notes := ExpandConcurrentAccessesWithNotes(prog, summaries)
	result := risksFromAccesses(accesses)
	result.Summary = buildConcurrencyRiskSummary(summaries, accesses)
	result.Notes = append(result.Notes, summarizeConcurrencyNotes(notes)...)
	if opts.IncludeDiagnostics {
		result.Notes = append(result.Notes, notes...)
		result.Notes = append(result.Notes, concurrencySummaryNotes(summaries)...)
	} else {
		result.Notes = append(result.Notes, concurrencySummaryNotes(summaries)...)
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

func buildConcurrencyRiskSummary(summaries map[*ssa.Function]FunctionAccessSummary, accesses []MemoryAccess) *ConcurrencyRiskSummary {
	if len(summaries) == 0 && len(accesses) == 0 {
		return &ConcurrencyRiskSummary{}
	}
	summary := &ConcurrencyRiskSummary{
		TotalRisks: len(accesses),
	}
	for _, fnSummary := range summaries {
		for _, note := range fnSummary.Notes {
			if strings.Contains(note, "unresolved dynamic call") {
				summary.UnresolvedDynamicCalls++
			}
			if strings.Contains(note, "unknown effects") {
				summary.UnknownEffects++
			}
		}
	}
	return summary
}

func summarizeConcurrencyNotes(notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	counts := map[string]int{}
	for _, note := range notes {
		switch {
		case strings.Contains(note, "unresolved dynamic goroutine call"):
			counts["unresolved dynamic goroutine call"]++
		case strings.Contains(note, "unresolved dynamic call"):
			counts["unresolved dynamic call"]++
		default:
			counts[note]++
		}
	}
	out := make([]string, 0, len(counts))
	for kind, count := range counts {
		if count == 1 {
			out = append(out, kind)
			continue
		}
		out = append(out, fmt.Sprintf("%s (%d occurrences)", kind, count))
	}
	sort.Strings(out)
	return out
}

func concurrencySummaryNotes(summaries map[*ssa.Function]FunctionAccessSummary) []string {
	seen := make(map[string]bool)
	var notes []string
	for _, summary := range summaries {
		for _, note := range summary.Notes {
			if seen[note] {
				continue
			}
			seen[note] = true
			notes = append(notes, note)
		}
	}
	sort.Strings(notes)
	return notes
}

func risksFromAccesses(accesses []MemoryAccess) *ConcurrencyRiskResult {
	result := &ConcurrencyRiskResult{}
	seen := make(map[string]bool)
	for _, access := range accesses {
		if access.InGoroutine && access.Kind == AccessWrite && !access.Atomic && !hasRelevantLock(access) && isSharedGoroutineAccess(access) {
			risk := accessRisk(access, access)
			key := fmt.Sprintf("%s:%d:%s:%s:%s", risk.File, risk.Line, risk.RiskLevel, risk.Struct, risk.Field)
			if !seen[key] {
				seen[key] = true
				result.Risks = append(result.Risks, risk)
			}
		}
	}
	for i := range accesses {
		for j := i + 1; j < len(accesses); j++ {
			a := accesses[i]
			b := accesses[j]
			if !mayRunConcurrently(a, b) {
				continue
			}
			if a.Kind != AccessWrite && b.Kind != AccessWrite {
				continue
			}
			if !sameOrUnknownMemory(a.Key, b.Key) {
				continue
			}
			if a.Atomic && b.Atomic {
				continue
			}
			if locksetsProtect(a, b) {
				continue
			}
			report := a
			other := b
			if report.Kind != AccessWrite {
				report, other = b, a
			}
			risk := accessRisk(report, other)
			key := fmt.Sprintf("%s:%d:%s:%s:%s", risk.File, risk.Line, risk.RiskLevel, risk.Struct, risk.Field)
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Risks = append(result.Risks, risk)
		}
	}
	return result
}

func isSharedGoroutineAccess(access MemoryAccess) bool {
	if access.Key.RootKind == RootUnknown && len(access.Key.FieldPath) == 0 {
		return false
	}
	switch access.Key.RootKind {
	case RootParam, RootReceiver, RootFreeVar, RootGlobal, RootUnknown:
		return true
	default:
		return false
	}
}

func hasRelevantLock(access MemoryAccess) bool {
	for _, lock := range access.Lockset {
		if isReadLock(lock) {
			continue
		}
		if lock.RootKind == access.Key.RootKind && lock.RootID == access.Key.RootID {
			return true
		}
	}
	return false
}

func mayRunConcurrently(a, b MemoryAccess) bool {
	if a.GoroutineID == "" && b.GoroutineID == "" {
		return false
	}
	if a.GoroutineID != "" && b.GoroutineID != "" && a.GoroutineID == b.GoroutineID {
		return false
	}
	return true
}

func sameOrUnknownMemory(a, b MemoryKey) bool {
	if sameMemoryKey(a, b) {
		return true
	}
	if a.RootKind == RootUnknown || b.RootKind == RootUnknown {
		if a.TypeName != "" && b.TypeName != "" && a.TypeName != b.TypeName {
			return false
		}
		return relatedMemoryRoots(a, b) && fieldPathsOverlap(a.FieldPath, b.FieldPath)
	}
	return false
}

func relatedMemoryRoots(a, b MemoryKey) bool {
	if a.RootKind == b.RootKind && a.RootID == b.RootID {
		return true
	}
	left := normalizedMemoryRootID(a.RootID)
	right := normalizedMemoryRootID(b.RootID)
	return left != "" && left == right
}

func normalizedMemoryRootID(rootID string) string {
	for _, prefix := range []string{"container:", "ambiguous:", "field-depth:", "dynamic:"} {
		rootID = strings.TrimPrefix(rootID, prefix)
	}
	return rootID
}

func fieldPathsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	for i := 0; i < min; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func locksetsProtect(a, b MemoryAccess) bool {
	for _, left := range a.Lockset {
		for _, right := range b.Lockset {
			if sameLockKey(left, right) && lockModesProtect(a.Kind, left, b.Kind, right) {
				return true
			}
		}
	}
	return false
}

func lockModesProtect(leftKind AccessKind, leftLock MemoryKey, rightKind AccessKind, rightLock MemoryKey) bool {
	if leftKind == AccessWrite && isReadLock(leftLock) {
		return false
	}
	if rightKind == AccessWrite && isReadLock(rightLock) {
		return false
	}
	return true
}

func sameLockKey(a, b MemoryKey) bool {
	a.RootID = lockBaseRootID(a.RootID)
	b.RootID = lockBaseRootID(b.RootID)
	return sameMemoryKey(a, b)
}

const readLockPrefix = "rlock:"

func readLockRootID(rootID string) string {
	return readLockPrefix + rootID
}

func isReadLock(key MemoryKey) bool {
	return strings.HasPrefix(key.RootID, readLockPrefix)
}

func lockBaseRootID(rootID string) string {
	return strings.TrimPrefix(rootID, readLockPrefix)
}

func accessRisk(access, other MemoryAccess) ConcurrencyRisk {
	level := "High"
	if access.Key.RootKind == RootUnknown || other.Key.RootKind == RootUnknown {
		level = "Medium"
	}
	if sameMemoryKey(access.Key, other.Key) && access.Key.RootKind != RootUnknown {
		level = "High"
	}
	field := ""
	if len(access.Key.FieldPath) > 0 {
		field = access.Key.FieldPath[len(access.Key.FieldPath)-1]
	} else if len(other.Key.FieldPath) > 0 {
		field = other.Key.FieldPath[len(other.Key.FieldPath)-1]
	}
	structName := access.Key.TypeName
	if structName == "" {
		structName = other.Key.TypeName
	}
	reason := fmt.Sprintf("Field %s.%s is accessed in a goroutine or concurrent context without a common lockset: %s at %s:%d and %s at %s:%d.", structName, field, access.Kind, access.File, access.Line, other.Kind, other.File, other.Line)
	return ConcurrencyRisk{
		RiskLevel: level,
		Struct:    structName,
		Field:     field,
		Function:  access.Function,
		File:      access.File,
		Line:      access.Line,
		Reasoning: reason,
		Anchor:    contextAnchor(access.File, access.Line, shortFuncName(access.Function)),
	}
}
