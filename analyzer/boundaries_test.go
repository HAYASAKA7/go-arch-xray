package analyzer

import (
	"testing"
)

func TestCheckArchitectureBoundaries_ForbidDirectImport(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_forbid", map[string]string{
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"infra/i.go":  "package infra\n\nimport \"bound_forbid/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"api/a.go":    "package api\n\nimport \"bound_forbid/domain\"\n\nfunc Get() string { return domain.Name() }\n",
	})

	ws := NewWorkspace()
	rules := []BoundaryRule{
		{Type: RuleForbid, From: "bound_forbid/infra", To: "bound_forbid/domain"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ViolationCount != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
	}
	v := result.Violations[0]
	if v.From != "bound_forbid/infra" {
		t.Errorf("expected violation from bound_forbid/infra, got %s", v.From)
	}
	if v.Import != "bound_forbid/domain" {
		t.Errorf("expected violation import bound_forbid/domain, got %s", v.Import)
	}
	if v.Rule != "forbid" {
		t.Errorf("expected rule=forbid, got %s", v.Rule)
	}
}

func TestCheckArchitectureBoundaries_ForbidPrefixPattern(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_forbid_prefix", map[string]string{
		"internal/core/c.go": "package core\n\nfunc Core() string { return \"core\" }\n",
		"internal/db/d.go":   "package db\n\nfunc DB() string { return \"db\" }\n",
		"internal/api/a.go":  "package api\n\nimport (\n\t\"bound_forbid_prefix/internal/core\"\n\t\"bound_forbid_prefix/internal/db\"\n)\n\nfunc Run() string { return core.Core() + db.DB() }\n",
	})

	ws := NewWorkspace()
	// api is forbidden from importing anything under internal/db/
	rules := []BoundaryRule{
		{Type: RuleForbid, From: "bound_forbid_prefix/internal/api", To: "bound_forbid_prefix/internal/db/"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ViolationCount != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
	}
	if result.Violations[0].Import != "bound_forbid_prefix/internal/db" {
		t.Errorf("expected db import as violation, got %s", result.Violations[0].Import)
	}
}

func TestCheckArchitectureBoundaries_AllowOnlyViolation(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_allow_only", map[string]string{
		"service/s.go": "package service\n\nfunc Svc() string { return \"svc\" }\n",
		"db/d.go":      "package db\n\nfunc DB() string { return \"db\" }\n",
		"api/a.go":     "package api\n\nimport (\n\t\"bound_allow_only/service\"\n\t\"bound_allow_only/db\"\n)\n\nfunc Handle() string { return service.Svc() + db.DB() }\n",
	})

	ws := NewWorkspace()
	// api may only import service (not db)
	rules := []BoundaryRule{
		{Type: RuleAllowOnly, From: "bound_allow_only/api", To: "bound_allow_only/service"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ViolationCount != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
	}
	if result.Violations[0].Import != "bound_allow_only/db" {
		t.Errorf("expected db as violation, got %s", result.Violations[0].Import)
	}
}

func TestCheckArchitectureBoundaries_AllowPrefixPermits(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_allow_prefix", map[string]string{
		"internal/svc/s.go":  "package svc\n\nfunc Run() string { return \"svc\" }\n",
		"internal/repo/r.go": "package repo\n\nfunc Load() string { return \"repo\" }\n",
		"api/a.go":           "package api\n\nimport (\n\t\"bound_allow_prefix/internal/svc\"\n\t\"bound_allow_prefix/internal/repo\"\n)\n\nfunc Handle() string { return svc.Run() + repo.Load() }\n",
	})

	ws := NewWorkspace()
	// api may only import packages with prefix bound_allow_prefix/internal/
	rules := []BoundaryRule{
		{Type: RuleAllowPrefix, From: "bound_allow_prefix/api", To: "bound_allow_prefix/internal/"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ViolationCount != 0 {
		t.Fatalf("expected 0 violations, got %d: %+v", result.ViolationCount, result.Violations)
	}
}

func TestCheckArchitectureBoundaries_AllowPrefixViolation(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_allow_prefix_v", map[string]string{
		"internal/svc/s.go": "package svc\n\nfunc Run() string { return \"svc\" }\n",
		"external/e.go":     "package external\n\nfunc Ext() string { return \"ext\" }\n",
		"api/a.go":          "package api\n\nimport (\n\t\"bound_allow_prefix_v/internal/svc\"\n\t\"bound_allow_prefix_v/external\"\n)\n\nfunc Handle() string { return svc.Run() + external.Ext() }\n",
	})

	ws := NewWorkspace()
	rules := []BoundaryRule{
		{Type: RuleAllowPrefix, From: "bound_allow_prefix_v/api", To: "bound_allow_prefix_v/internal/"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ViolationCount != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", result.ViolationCount, result.Violations)
	}
	if result.Violations[0].Import != "bound_allow_prefix_v/external" {
		t.Errorf("expected external as violation, got %s", result.Violations[0].Import)
	}
}

func TestCheckArchitectureBoundaries_NoRulesReturnsEmpty(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_norules", map[string]string{
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n",
	})

	ws := NewWorkspace()
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ViolationCount != 0 {
		t.Fatalf("expected 0 violations, got %d", result.ViolationCount)
	}
}

func TestCheckArchitectureBoundaries_StdlibImportsNotViolated(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_stdlib", map[string]string{
		"app/a.go": "package app\n\nimport \"fmt\"\n\nfunc Run() { fmt.Println(\"hi\") }\n",
	})

	ws := NewWorkspace()
	// allow_only with a non-stdlib target — stdlib should not count as violation
	rules := []BoundaryRule{
		{Type: RuleAllowOnly, From: "bound_stdlib/app", To: "bound_stdlib/domain"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// fmt is stdlib, so should not be a violation
	for _, v := range result.Violations {
		if v.Import == "fmt" {
			t.Fatalf("stdlib import fmt should not be a violation")
		}
	}
}

func TestCheckArchitectureBoundaries_ViolationHasSourceLocation(t *testing.T) {
	dir := createDependencyTestModule(t, "bound_loc", map[string]string{
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"infra/i.go":  "package infra\n\nimport \"bound_loc/domain\"\n\nfunc Run() string { return domain.Name() }\n",
	})

	ws := NewWorkspace()
	rules := []BoundaryRule{
		{Type: RuleForbid, From: "bound_loc/infra", To: "bound_loc/domain"},
	}
	result, err := CheckArchitectureBoundaries(ws, dir, "./...", rules)
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
}
