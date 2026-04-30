package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// MethodFingerprint is the per-function record cached during load. It captures
// just enough source information to detect duplicate implementations across
// the workspace without retaining the full AST. Stored on LoadedProgram so
// FindDuplicateMethods does not have to re-parse files.
type MethodFingerprint struct {
	Function  string // package.Receiver.Name (or package.Function for free functions)
	Package   string
	Receiver  string // empty for free functions
	Name      string
	Signature string // normalized parameter+result types, e.g. "(int) (string, error)"
	BodyHash  string // SHA-256 of pretty-printed body (whitespace-normalized)
	BodyLines int    // body length in source lines (for caller reporting / triage)
	File      string
	Line      int
}

// extractMethodFingerprintsFromSyntax walks every package's syntax and emits
// a fingerprint per top-level function/method declaration with a non-empty
// body. Generic type parameters are intentionally ignored when hashing the
// body — instantiation differences are handled by the call graph elsewhere.
func extractMethodFingerprintsFromSyntax(pkgs []*packages.Package) []MethodFingerprint {
	var out []MethodFingerprint
	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil || fd.Name == nil {
					continue
				}
				fp := buildMethodFingerprint(pkg, fd)
				if fp == nil {
					continue
				}
				out = append(out, *fp)
			}
		}
	}
	return out
}

func buildMethodFingerprint(pkg *packages.Package, fd *ast.FuncDecl) *MethodFingerprint {
	pos := pkg.Fset.Position(fd.Pos())
	if pos.Filename == "" {
		return nil
	}
	receiver := receiverName(fd)
	signature := normalizedSignature(fd.Type)
	bodyHash, bodyLines := hashFuncBody(pkg.Fset, fd.Body)

	qualified := pkg.PkgPath + "."
	if receiver != "" {
		qualified += receiver + "."
	}
	qualified += fd.Name.Name

	return &MethodFingerprint{
		Function:  qualified,
		Package:   pkg.PkgPath,
		Receiver:  receiver,
		Name:      fd.Name.Name,
		Signature: signature,
		BodyHash:  bodyHash,
		BodyLines: bodyLines,
		File:      pos.Filename,
		Line:      pos.Line,
	}
}

// receiverName returns the receiver type name for a method declaration, e.g.
// "Server" or "*Server" → "Server". Returns empty for free functions.
func receiverName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	expr := fd.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.IndexListExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// normalizedSignature pretty-prints the function type without the receiver
// or function name, yielding a stable string key for grouping. Whitespace is
// collapsed so formatting differences don't create false negatives.
func normalizedSignature(ft *ast.FuncType) string {
	if ft == nil {
		return ""
	}
	var b strings.Builder
	fset := token.NewFileSet()
	cfg := printer.Config{Mode: printer.RawFormat, Tabwidth: 1}
	// Print parameters and results separately to avoid printing the keyword "func".
	if ft.Params != nil {
		_ = cfg.Fprint(&b, fset, ft.Params)
	}
	if ft.Results != nil {
		b.WriteByte(' ')
		_ = cfg.Fprint(&b, fset, ft.Results)
	}
	return collapseWhitespace(b.String())
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// hashFuncBody returns a stable hash of the function body and its source line
// span. The body is pretty-printed without comments so formatting and comment
// differences do not affect duplicate detection.
func hashFuncBody(fset *token.FileSet, body *ast.BlockStmt) (string, int) {
	if body == nil {
		return "", 0
	}
	var b strings.Builder
	cfg := printer.Config{Mode: printer.RawFormat, Tabwidth: 1}
	_ = cfg.Fprint(&b, fset, body)
	normalized := collapseWhitespace(b.String())
	sum := sha256.Sum256([]byte(normalized))

	startLine := fset.Position(body.Lbrace).Line
	endLine := fset.Position(body.Rbrace).Line
	lines := endLine - startLine + 1
	if lines < 0 {
		lines = 0
	}
	return hex.EncodeToString(sum[:]), lines
}

// DuplicateMethodLocation describes one occurrence of a duplicated body.
type DuplicateMethodLocation struct {
	Function string `json:"function"`
	Package  string `json:"package"`
	Receiver string `json:"receiver,omitempty"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Anchor   string `json:"context_anchor,omitempty"`
}

// DuplicateMethodGroup groups together every function whose body normalizes
// to the same hash AND whose signature matches.
type DuplicateMethodGroup struct {
	Signature string                    `json:"signature"`
	BodyHash  string                    `json:"body_hash"`
	BodyLines int                       `json:"body_lines"`
	Count     int                       `json:"count"`
	Locations []DuplicateMethodLocation `json:"locations"`
}

// DuplicateMethodsResult is returned by FindDuplicateMethods.
type DuplicateMethodsResult struct {
	Groups       []DuplicateMethodGroup `json:"groups"`
	Total        int                    `json:"total_groups"`
	MinBodyLines int                    `json:"min_body_lines"`
	Notes        []string               `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

// DuplicateMethodsOptions tunes the duplicate scan.
type DuplicateMethodsOptions struct {
	// MinBodyLines filters out short bodies that are likely to collide
	// trivially (e.g. one-line getters). Default 3 when zero.
	MinBodyLines int
}

// FindDuplicateMethods reports groups of functions/methods whose body and
// signature match across the loaded workspace. Method bodies are normalized
// to whitespace-collapsed pretty-printed source before hashing. Comments are
// not part of the hash, so commented-out variants of the same logic still
// group together.
func FindDuplicateMethods(ws *Workspace, dir, pattern string) (*DuplicateMethodsResult, error) {
	return FindDuplicateMethodsWithOptions(ws, dir, pattern, DuplicateMethodsOptions{}, QueryOptions{})
}

// FindDuplicateMethodsWithOptions is the streaming/paginated variant.
func FindDuplicateMethodsWithOptions(ws *Workspace, dir, pattern string, dmOpts DuplicateMethodsOptions, opts QueryOptions) (*DuplicateMethodsResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	minLines := dmOpts.MinBodyLines
	if minLines <= 0 {
		minLines = 3
	}

	type groupKey struct {
		signature string
		hash      string
	}
	buckets := make(map[groupKey][]MethodFingerprint, 64)
	for _, fp := range prog.methodFingerprints {
		if fp.BodyHash == "" || fp.BodyLines < minLines {
			continue
		}
		k := groupKey{signature: fp.Signature, hash: fp.BodyHash}
		buckets[k] = append(buckets[k], fp)
	}

	groups := make([]DuplicateMethodGroup, 0, len(buckets))
	for k, members := range buckets {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			if members[i].Package != members[j].Package {
				return members[i].Package < members[j].Package
			}
			if members[i].Function != members[j].Function {
				return members[i].Function < members[j].Function
			}
			return members[i].File < members[j].File
		})
		locs := make([]DuplicateMethodLocation, 0, len(members))
		for _, m := range members {
			locs = append(locs, DuplicateMethodLocation{
				Function: m.Function,
				Package:  m.Package,
				Receiver: m.Receiver,
				Name:     m.Name,
				File:     m.File,
				Line:     m.Line,
				Anchor:   contextAnchor(m.File, m.Line, m.Name),
			})
		}
		groups = append(groups, DuplicateMethodGroup{
			Signature: k.signature,
			BodyHash:  k.hash,
			BodyLines: members[0].BodyLines,
			Count:     len(members),
			Locations: locs,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count // largest groups first
		}
		if groups[i].BodyLines != groups[j].BodyLines {
			return groups[i].BodyLines > groups[j].BodyLines // longer bodies = more impactful
		}
		return groups[i].BodyHash < groups[j].BodyHash
	})

	result := &DuplicateMethodsResult{
		Groups:       groups,
		Total:        len(groups),
		MinBodyLines: minLines,
		Notes: []string{
			"Bodies are compared after whitespace normalization and comment stripping; identifier renames still count as distinct.",
			"Generic type parameter differences do NOT split groups — a duplicate body across two type parameters is reported once.",
			"Short bodies are filtered by min_body_lines (default 3) to avoid trivial getter/setter collisions.",
			"Test files (*_test.go) are not loaded into the analysis program; duplicates inside tests are not reported.",
		},
	}

	result.TotalBeforeTruncate = result.Total
	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Groups, _, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Groups, "duplicate_methods:"+dir+"|"+pattern, duplicateGroupKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	return result, nil
}

func duplicateGroupKey(g DuplicateMethodGroup) string {
	return g.BodyHash + "|" + g.Signature
}
