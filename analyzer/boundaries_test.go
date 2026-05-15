package analyzer

import "testing"

func TestCheckArchitectureBoundaries_BatchedRules(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_rules", map[string]string{
		"domain/d.go":       "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"infra/one/i.go":    "package one\n\nimport \"bound_rules/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"infra/two/i.go":    "package two\n\nimport \"bound_rules/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"infra/three/i.go":  "package three\n\nimport \"bound_rules/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"api/a.go":          "package api\n\nimport (\n\t\"bound_rules/domain\"\n\t\"bound_rules/external\"\n\t\"bound_rules/internal/db\"\n\t\"bound_rules/internal/svc\"\n)\n\nfunc Get() string { return domain.Name() + external.Ext() + db.DB() + svc.Run() }\n",
		"external/e.go":     "package external\n\nfunc Ext() string { return \"ext\" }\n",
		"internal/db/d.go":  "package db\n\nfunc DB() string { return \"db\" }\n",
		"internal/svc/s.go": "package svc\n\nfunc Run() string { return \"svc\" }\n",
		"stdlib/s.go":       "package stdlib\n\nimport \"fmt\"\n\nfunc Run() { fmt.Println(\"hi\") }\n",
	})

	ws := newTestWorkspace(t)

	t.Run("forbid direct import", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleForbid, From: "bound_rules/infra/one", To: "bound_rules/domain"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 1 {
			t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
		}
		v := result.Violations[0]
		if v.From != "bound_rules/infra/one" {
			t.Errorf("expected violation from bound_rules/infra/one, got %s", v.From)
		}
		if v.Import != "bound_rules/domain" {
			t.Errorf("expected violation import bound_rules/domain, got %s", v.Import)
		}
		if v.Rule != "forbid" {
			t.Errorf("expected rule=forbid, got %s", v.Rule)
		}
	})

	t.Run("forbid prefix pattern", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleForbid, From: "bound_rules/api", To: "bound_rules/internal/db/"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 1 {
			t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
		}
		if result.Violations[0].Import != "bound_rules/internal/db" {
			t.Errorf("expected db import as violation, got %s", result.Violations[0].Import)
		}
	})

	t.Run("allow only violation", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleAllowOnly, From: "bound_rules/api", To: "bound_rules/internal/svc"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 3 {
			t.Fatalf("expected 3 violations, got %d: %+v", result.ViolationCount, result.Violations)
		}
		if !hasBoundaryViolation(result, "bound_rules/api", "bound_rules/domain") {
			t.Fatal("expected domain as allow_only violation")
		}
	})

	t.Run("allow prefix permits", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleAllowPrefix, From: "bound_rules/api", To: "bound_rules/"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 0 {
			t.Fatalf("expected 0 violations, got %d: %+v", result.ViolationCount, result.Violations)
		}
	})

	t.Run("allow prefix violation", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleAllowPrefix, From: "bound_rules/api", To: "bound_rules/internal/"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 2 {
			t.Fatalf("expected 2 violations, got %d: %+v", result.ViolationCount, result.Violations)
		}
		if !hasBoundaryViolation(result, "bound_rules/api", "bound_rules/external") {
			t.Fatal("expected external as allow_prefix violation")
		}
	})

	t.Run("location", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleForbid, From: "bound_rules/infra/one", To: "bound_rules/domain"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ViolationCount != 1 {
			t.Fatalf("expected 1 violation")
		}
		v := result.Violations[0]
		if v.File == "" || v.Line == 0 {
			t.Errorf("expected violation to have file/line location, got file=%q line=%d", v.File, v.Line)
		}
	})

	t.Run("limit offset", func(t *testing.T) {
		result, err := CheckArchitectureBoundariesWithOptions(ws, dir, "./...", []BoundaryRule{
			{Type: RuleForbid, From: "bound_rules/infra/", To: "bound_rules/domain"},
		}, QueryOptions{
			Limit:  1,
			Offset: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.TotalBeforeTruncate != 3 {
			t.Fatalf("expected 3 total violations before truncate, got %d", result.TotalBeforeTruncate)
		}
		if len(result.Violations) != 1 {
			t.Fatalf("expected 1 violation due to limit, got %d", len(result.Violations))
		}
		if !result.Truncated {
			t.Fatal("expected truncated to be true")
		}
		if result.Violations[0].From != "bound_rules/infra/three" {
			t.Fatalf("expected bound_rules/infra/three at offset 1, got %s", result.Violations[0].From)
		}
	})

	t.Run("stdlib imports not violated", func(t *testing.T) {
		result, err := CheckArchitectureBoundaries(ws, dir, "./...", []BoundaryRule{
			{Type: RuleAllowOnly, From: "bound_rules/stdlib", To: "bound_rules/domain"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, v := range result.Violations {
			if v.Import == "fmt" {
				t.Fatalf("stdlib import fmt should not be a violation")
			}
		}
	})
}

func TestCheckArchitectureBoundaries_NoRulesReturnsEmpty(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_norules", map[string]string{
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n",
	})

	ws := newTestWorkspace(t)
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ViolationCount != 0 {
		t.Fatalf("expected 0 violations, got %d", result.ViolationCount)
	}
}

func hasBoundaryViolation(result *BoundaryResult, from, imp string) bool {
	for _, v := range result.Violations {
		if v.From == from && v.Import == imp {
			return true
		}
	}
	return false
}
