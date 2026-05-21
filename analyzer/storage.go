package analyzer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

const workspaceStoreSchemaVersion = "1"
const codeSymbolVectorTable = "code_symbol_vec"

type WorkspaceStore struct {
	Layout WorkspaceLayout
	DB     *sql.DB
}

type FileMeta struct {
	Path           string
	Hash           string
	Module         string
	Package        string
	Size           int64
	ModifiedAt     time.Time
	IndexedAt      time.Time
	LastBuildSSAAt time.Time
}

type SymbolHash struct {
	SymbolID         string
	FilePath         string
	SymbolType       string
	SymbolName       string
	SourceHash       string
	EmbeddingVersion int
	CreatedAt        time.Time
}

type ArchEdge struct {
	SourceType string
	SourcePath string
	SourceName string
	TargetType string
	TargetPath string
	TargetName string
	EdgeType   string
	Metadata   string
	CreatedAt  time.Time
}

type PackageDependencyRow struct {
	Package string
	Imports []string
}

type HTTPRouteRow struct {
	Method    string
	Path      string
	Handler   string
	Framework string
	File      string
	Line      int
}

type GRPCEndpointRow struct {
	Endpoint     GRPCEndpoint
	Registration GRPCRegistration
	Kind         string
}

type CodeSymbol struct {
	ID          string
	Type        string
	PackagePath string
	Name        string
	FilePath    string
	LineStart   int
	LineEnd     int
	Source      string
	Embedding   []byte
}

func OpenWorkspaceStore(root string) (*WorkspaceStore, error) {
	layout := WorkspaceLayoutFor(root)
	if err := layout.EnsureExists(); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", layout.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable wal: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set synchronous: %w", err)
	}

	store := &WorkspaceStore{Layout: layout, DB: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *WorkspaceStore) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *WorkspaceStore) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS file_meta (
			id INTEGER PRIMARY KEY,
			path TEXT UNIQUE NOT NULL,
			hash TEXT NOT NULL,
			module TEXT NOT NULL,
			package TEXT NOT NULL,
			size INTEGER,
			modified_at TIMESTAMP,
			indexed_at TIMESTAMP,
			last_build_ssa_at TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_file_meta_hash ON file_meta(hash);`,
		`CREATE INDEX IF NOT EXISTS idx_file_meta_path ON file_meta(path);`,
		`CREATE TABLE IF NOT EXISTS symbol_hashes (
			symbol_id TEXT PRIMARY KEY,
			file_path TEXT NOT NULL,
			symbol_type TEXT NOT NULL,
			symbol_name TEXT NOT NULL,
			source_hash TEXT NOT NULL,
			embedding_version INTEGER,
			created_at TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_symbol_hashes_file ON symbol_hashes(file_path);`,
		`CREATE TABLE IF NOT EXISTS arch_edges (
			id INTEGER PRIMARY KEY,
			source_type TEXT NOT NULL,
			source_path TEXT NOT NULL,
			source_name TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_path TEXT NOT NULL,
			target_name TEXT NOT NULL,
			edge_type TEXT NOT NULL,
			metadata JSON,
			created_at TIMESTAMP,
			UNIQUE(source_type, source_path, source_name, target_type, target_path, target_name, edge_type)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source ON arch_edges(source_path, edge_type);`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target ON arch_edges(target_path, edge_type);`,
		`CREATE TABLE IF NOT EXISTS complexity_metrics (
			function_id TEXT PRIMARY KEY,
			package_path TEXT NOT NULL,
			function_name TEXT NOT NULL,
			receiver TEXT,
			file_path TEXT NOT NULL,
			line_start INTEGER,
			cyclomatic INTEGER,
			cognitive INTEGER,
			body_lines INTEGER,
			max_nesting INTEGER,
			halstead_volume REAL,
			halstead_difficulty REAL,
			halstead_effort REAL,
			maintainability_index REAL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_complexity_pkg ON complexity_metrics(package_path);`,
		`CREATE TABLE IF NOT EXISTS http_routes (
			id INTEGER PRIMARY KEY,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			handler_path TEXT,
			handler_name TEXT,
			framework TEXT,
			file_path TEXT,
			line INTEGER,
			UNIQUE(method, path, handler_name, framework, file_path, line)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_routes_path ON http_routes(path);`,
		`CREATE TABLE IF NOT EXISTS grpc_endpoints (
			id INTEGER PRIMARY KEY,
			service TEXT NOT NULL,
			method TEXT NOT NULL,
			full_path TEXT NOT NULL,
			rpc_type TEXT NOT NULL,
			handler_path TEXT,
			handler_type TEXT,
			service_desc TEXT,
			proto_file TEXT,
			package_path TEXT,
			registered INTEGER,
			implementations TEXT,
			file_path TEXT,
			line INTEGER,
			UNIQUE(service, method, rpc_type, package_path, file_path, line)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_grpc_full_path ON grpc_endpoints(full_path);`,
		`CREATE TABLE IF NOT EXISTS grpc_registrations (
			id INTEGER PRIMARY KEY,
			service TEXT,
			service_short_name TEXT,
			register_func TEXT NOT NULL,
			registrar TEXT,
			implementation TEXT,
			package_path TEXT,
			file_path TEXT,
			line INTEGER,
			UNIQUE(register_func, package_path, file_path, line)
		);`,
		`CREATE TABLE IF NOT EXISTS code_symbols (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			package_path TEXT NOT NULL,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			line_start INTEGER,
			line_end INTEGER,
			source TEXT NOT NULL,
			embedding BLOB
		);`,
		`CREATE INDEX IF NOT EXISTS idx_code_symbols_pkg ON code_symbols(package_path);`,
		`CREATE TABLE IF NOT EXISTS code_symbol_vec_map (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol_id TEXT UNIQUE NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS system_state (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at TIMESTAMP
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO system_state(key, value, updated_at)
		VALUES('schema_version', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		workspaceStoreSchemaVersion, time.Now().UTC()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

func (s *WorkspaceStore) BeginTx() (*sql.Tx, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("workspace store is not open")
	}
	return s.DB.Begin()
}

func (s *WorkspaceStore) InTransaction(fn func(*sql.Tx) error) error {
	tx, err := s.BeginTx()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *WorkspaceStore) StoreFileMeta(meta FileMeta) error {
	if meta.IndexedAt.IsZero() {
		meta.IndexedAt = time.Now().UTC()
	}
	_, err := s.DB.Exec(`INSERT INTO file_meta(path, hash, module, package, size, modified_at, indexed_at, last_build_ssa_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash,
			module=excluded.module,
			package=excluded.package,
			size=excluded.size,
			modified_at=excluded.modified_at,
			indexed_at=excluded.indexed_at,
			last_build_ssa_at=excluded.last_build_ssa_at`,
		meta.Path, meta.Hash, meta.Module, meta.Package, meta.Size, meta.ModifiedAt, meta.IndexedAt, nullableTime(meta.LastBuildSSAAt))
	return err
}

func (s *WorkspaceStore) GetFileByHash(hash string) (FileMeta, error) {
	row := s.DB.QueryRow(`SELECT path, hash, module, package, size, modified_at, indexed_at, last_build_ssa_at
		FROM file_meta WHERE hash = ? ORDER BY path LIMIT 1`, hash)
	return scanFileMeta(row)
}

func (s *WorkspaceStore) GetFileByPath(path string) (FileMeta, error) {
	row := s.DB.QueryRow(`SELECT path, hash, module, package, size, modified_at, indexed_at, last_build_ssa_at
		FROM file_meta WHERE path = ? LIMIT 1`, path)
	return scanFileMeta(row)
}

func (s *WorkspaceStore) GetFilesByPrefix(prefix string) ([]FileMeta, error) {
	rows, err := s.DB.Query(`SELECT path, hash, module, package, size, modified_at, indexed_at, last_build_ssa_at
		FROM file_meta WHERE path LIKE ? ORDER BY path`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileMeta
	for rows.Next() {
		meta, err := scanFileMeta(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, meta)
	}
	return files, rows.Err()
}

func (s *WorkspaceStore) DeleteFilesByPath(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`DELETE FROM file_meta WHERE path = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, path := range paths {
			if _, err := stmt.Exec(path); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *WorkspaceStore) UpsertEdges(edges []ArchEdge) error {
	if len(edges) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertEdgesTx(tx, edges)
	})
}

func (s *WorkspaceStore) upsertEdgesTx(tx *sql.Tx, edges []ArchEdge) error {
	stmt, err := tx.Prepare(`INSERT INTO arch_edges(source_type, source_path, source_name, target_type, target_path, target_name, edge_type, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_type, source_path, source_name, target_type, target_path, target_name, edge_type)
		DO UPDATE SET metadata=excluded.metadata, created_at=excluded.created_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, edge := range edges {
		if edge.CreatedAt.IsZero() {
			edge.CreatedAt = time.Now().UTC()
		}
		if _, err := stmt.Exec(edge.SourceType, edge.SourcePath, edge.SourceName, edge.TargetType, edge.TargetPath, edge.TargetName, edge.EdgeType, edge.Metadata, edge.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) GetEdges(source, edgeType string) ([]ArchEdge, error) {
	query := `SELECT source_type, source_path, source_name, target_type, target_path, target_name, edge_type, COALESCE(metadata, ''), created_at
		FROM arch_edges WHERE 1=1`
	var args []any
	if source != "" {
		query += ` AND source_path = ?`
		args = append(args, source)
	}
	if edgeType != "" {
		query += ` AND edge_type = ?`
		args = append(args, edgeType)
	}
	query += ` ORDER BY source_path, target_path, target_name`
	return s.queryEdges(query, args...)
}

func (s *WorkspaceStore) GetEdgesByPackage(pkgPath string) ([]ArchEdge, error) {
	return s.queryEdges(`SELECT source_type, source_path, source_name, target_type, target_path, target_name, edge_type, COALESCE(metadata, ''), created_at
		FROM arch_edges WHERE source_path = ? OR target_path = ? ORDER BY source_path, target_path, target_name`, pkgPath, pkgPath)
}

func (s *WorkspaceStore) GetPackageDependencies(includeStdlib bool) ([]PackageDependencyRow, error) {
	rows, err := s.DB.Query(`SELECT package FROM file_meta WHERE package <> ''
		UNION
		SELECT source_path FROM arch_edges WHERE edge_type = 'imports' AND source_path <> ''
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deps := make(map[string][]string)
	packages := make(map[string]bool)
	for rows.Next() {
		var pkg string
		if err := rows.Scan(&pkg); err != nil {
			return nil, err
		}
		if pkg == "" {
			continue
		}
		packages[pkg] = true
		deps[pkg] = nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	edgeRows, err := s.DB.Query(`SELECT source_path, COALESCE(NULLIF(target_path, ''), target_name)
		FROM arch_edges
		WHERE edge_type = 'imports'
		ORDER BY source_path, target_path`)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	rootPaths := make(map[string]bool)
	for edgeRows.Next() {
		var source, target string
		if err := edgeRows.Scan(&source, &target); err != nil {
			return nil, err
		}
		rootPaths[source] = true
		if source == "" {
			continue
		}
		deps[source] = append(deps[source], target)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}
	out := make([]PackageDependencyRow, 0, len(deps))
	for pkg, imports := range deps {
		uniq := make([]string, 0, len(imports))
		seen := make(map[string]bool, len(imports))
		for _, imp := range imports {
			if !includeStdlib && !rootPaths[imp] && isStdlib(imp) {
				continue
			}
			if seen[imp] {
				continue
			}
			seen[imp] = true
			uniq = append(uniq, imp)
		}
		sort.Strings(uniq)
		out = append(out, PackageDependencyRow{Package: pkg, Imports: uniq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
}

func (s *WorkspaceStore) GetPackageAnchor(pkgPath string) (string, int, error) {
	row := s.DB.QueryRow(`SELECT path FROM file_meta WHERE package = ? ORDER BY path LIMIT 1`, pkgPath)
	var file string
	if err := row.Scan(&file); err != nil {
		return "", 0, err
	}
	return file, 1, nil
}

func (s *WorkspaceStore) queryEdges(query string, args ...any) ([]ArchEdge, error) {
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []ArchEdge
	for rows.Next() {
		var edge ArchEdge
		if err := rows.Scan(&edge.SourceType, &edge.SourcePath, &edge.SourceName, &edge.TargetType, &edge.TargetPath, &edge.TargetName, &edge.EdgeType, &edge.Metadata, &edge.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (s *WorkspaceStore) UpsertMetrics(metrics []FunctionComplexity) error {
	if len(metrics) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertMetricsTx(tx, metrics)
	})
}

func (s *WorkspaceStore) upsertMetricsTx(tx *sql.Tx, metrics []FunctionComplexity) error {
	stmt, err := tx.Prepare(`INSERT INTO complexity_metrics(function_id, package_path, function_name, receiver, file_path, line_start, cyclomatic, cognitive, body_lines, max_nesting, halstead_volume, halstead_difficulty, halstead_effort, maintainability_index)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(function_id) DO UPDATE SET
			package_path=excluded.package_path,
			function_name=excluded.function_name,
			receiver=excluded.receiver,
			file_path=excluded.file_path,
			line_start=excluded.line_start,
			cyclomatic=excluded.cyclomatic,
			cognitive=excluded.cognitive,
			body_lines=excluded.body_lines,
			max_nesting=excluded.max_nesting,
			halstead_volume=excluded.halstead_volume,
			halstead_difficulty=excluded.halstead_difficulty,
			halstead_effort=excluded.halstead_effort,
			maintainability_index=excluded.maintainability_index`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, metric := range metrics {
		if _, err := stmt.Exec(metric.Function, metric.Package, metric.Name, metric.Receiver, metric.File, metric.Line, metric.Cyclomatic, metric.Cognitive, metric.BodyLines, metric.MaxNesting, metric.HalsteadVolume, metric.HalsteadDifficulty, metric.HalsteadEffort, metric.MaintainabilityIndex); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) GetMetrics(filters ComplexityOptions) ([]FunctionComplexity, error) {
	filters = normalizeComplexityOptions(filters)
	query := `SELECT function_id, package_path, function_name, receiver, file_path, line_start, cyclomatic, cognitive, body_lines, max_nesting, halstead_volume, halstead_difficulty, halstead_effort, maintainability_index FROM complexity_metrics WHERE 1=1`
	var args []any
	if filters.MinCyclomatic > 0 {
		query += ` AND cyclomatic >= ?`
		args = append(args, filters.MinCyclomatic)
	}
	if filters.MinCognitive > 0 {
		query += ` AND cognitive >= ?`
		args = append(args, filters.MinCognitive)
	}
	if filters.MinHalsteadVolume > 0 {
		query += ` AND halstead_volume >= ?`
		args = append(args, filters.MinHalsteadVolume)
	}
	if filters.MaxMaintainabilityIndex > 0 {
		query += ` AND maintainability_index <= ?`
		args = append(args, filters.MaxMaintainabilityIndex)
	}
	query += ` ORDER BY package_path, function_id`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var metrics []FunctionComplexity
	for rows.Next() {
		var metric FunctionComplexity
		if err := rows.Scan(&metric.Function, &metric.Package, &metric.Name, &metric.Receiver, &metric.File, &metric.Line, &metric.Cyclomatic, &metric.Cognitive, &metric.BodyLines, &metric.MaxNesting, &metric.HalsteadVolume, &metric.HalsteadDifficulty, &metric.HalsteadEffort, &metric.MaintainabilityIndex); err != nil {
			return nil, err
		}
		metric.Anchor = contextAnchor(metric.File, metric.Line, metric.Name)
		metrics = append(metrics, metric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortComplexityMetrics(metrics, filters.SortBy)
	return metrics, nil
}

func (s *WorkspaceStore) UpsertRoutes(routes []HTTPRoute) error {
	if len(routes) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertRoutesTx(tx, routes)
	})
}

func (s *WorkspaceStore) GetHTTPRoutes() ([]HTTPRouteRow, error) {
	rows, err := s.DB.Query(`SELECT method, path, COALESCE(handler_name, ''), COALESCE(framework, ''), COALESCE(file_path, ''), COALESCE(line, 0)
		FROM http_routes ORDER BY path, method, handler_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HTTPRouteRow
	for rows.Next() {
		var row HTTPRouteRow
		if err := rows.Scan(&row.Method, &row.Path, &row.Handler, &row.Framework, &row.File, &row.Line); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *WorkspaceStore) upsertRoutesTx(tx *sql.Tx, routes []HTTPRoute) error {
	stmt, err := tx.Prepare(`INSERT INTO http_routes(method, path, handler_path, handler_name, framework, file_path, line)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(method, path, handler_name, framework, file_path, line)
		DO UPDATE SET handler_path=excluded.handler_path`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, route := range routes {
		if _, err := stmt.Exec(route.Method, route.Path, "", route.Handler, route.Framework, route.File, route.Line); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) UpsertGRPCEndpoints(endpoints []GRPCEndpoint) error {
	if len(endpoints) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertGRPCEndpointsTx(tx, endpoints)
	})
}

func (s *WorkspaceStore) UpsertGRPCRegistrations(registrations []GRPCRegistration) error {
	if len(registrations) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertGRPCRegistrationsTx(tx, registrations)
	})
}

func (s *WorkspaceStore) upsertGRPCRegistrationsTx(tx *sql.Tx, registrations []GRPCRegistration) error {
	stmt, err := tx.Prepare(`INSERT INTO grpc_registrations(service, service_short_name, register_func, registrar, implementation, package_path, file_path, line)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(register_func, package_path, file_path, line)
			DO UPDATE SET
				service=excluded.service,
				service_short_name=excluded.service_short_name,
				registrar=excluded.registrar,
				implementation=excluded.implementation`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, registration := range registrations {
		if _, err := stmt.Exec(registration.Service, registration.ServiceShortName, registration.RegisterFunc, registration.Registrar, registration.Implementation, registration.Package, registration.File, registration.Line); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) GetGRPCEndpoints() ([]GRPCEndpoint, []GRPCRegistration, error) {
	rows, err := s.DB.Query(`SELECT service, method, full_path, rpc_type, handler_path, handler_type, service_desc, proto_file, package_path, registered, implementations, file_path, line
		FROM grpc_endpoints ORDER BY service, method, line`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var endpoints []GRPCEndpoint
	for rows.Next() {
		var endpoint GRPCEndpoint
		var registered int
		var impls string
		if err := rows.Scan(&endpoint.Service, &endpoint.Method, &endpoint.FullMethod, &endpoint.RPCType, &endpoint.Handler, &endpoint.HandlerType, &endpoint.ServiceDesc, &endpoint.ProtoFile, &endpoint.Package, &registered, &impls, &endpoint.File, &endpoint.Line); err != nil {
			return nil, nil, err
		}
		if idx := strings.Index(endpoint.ProtoFile, "\nshort_name:"); idx >= 0 {
			endpoint.ServiceShortName = strings.TrimSpace(endpoint.ProtoFile[idx+len("\nshort_name:"):])
			endpoint.ProtoFile = endpoint.ProtoFile[:idx]
		}
		if endpoint.ServiceShortName == "" {
			endpoint.ServiceShortName = grpcShortNameFromService(endpoint.Service)
		}
		endpoint.Registered = registered != 0
		if strings.TrimSpace(impls) != "" {
			endpoint.Implementations = strings.Split(impls, "\n")
		}
		endpoint.Anchor = contextAnchor(endpoint.File, endpoint.Line, endpoint.Method)
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	regRows, err := s.DB.Query(`SELECT COALESCE(service, ''), COALESCE(service_short_name, ''), register_func, COALESCE(registrar, ''), COALESCE(implementation, ''), COALESCE(package_path, ''), COALESCE(file_path, ''), COALESCE(line, 0)
		FROM grpc_registrations ORDER BY service, register_func, line`)
	if err != nil {
		return nil, nil, err
	}
	defer regRows.Close()
	var registrations []GRPCRegistration
	for regRows.Next() {
		var registration GRPCRegistration
		if err := regRows.Scan(&registration.Service, &registration.ServiceShortName, &registration.RegisterFunc, &registration.Registrar, &registration.Implementation, &registration.Package, &registration.File, &registration.Line); err != nil {
			return nil, nil, err
		}
		registration.Anchor = contextAnchor(registration.File, registration.Line, registration.RegisterFunc)
		registrations = append(registrations, registration)
	}
	if err := regRows.Err(); err != nil {
		return nil, nil, err
	}
	return endpoints, registrations, nil
}

func (s *WorkspaceStore) upsertGRPCEndpointsTx(tx *sql.Tx, endpoints []GRPCEndpoint) error {
	stmt, err := tx.Prepare(`INSERT INTO grpc_endpoints(service, method, full_path, rpc_type, handler_path, handler_type, service_desc, proto_file, package_path, registered, implementations, file_path, line)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(service, method, rpc_type, package_path, file_path, line)
		DO UPDATE SET
			full_path=excluded.full_path,
			handler_path=excluded.handler_path,
			handler_type=excluded.handler_type,
			service_desc=excluded.service_desc,
			proto_file=excluded.proto_file,
			registered=excluded.registered,
			implementations=excluded.implementations`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, endpoint := range endpoints {
		registered := 0
		if endpoint.Registered {
			registered = 1
		}
		serviceShortName := endpoint.ServiceShortName
		if serviceShortName == "" {
			serviceShortName = grpcShortNameFromService(endpoint.Service)
		}
		protoFile := endpoint.ProtoFile
		if serviceShortName != "" {
			protoFile = protoFile + "\nshort_name:" + serviceShortName
		}
		if _, err := stmt.Exec(endpoint.Service, endpoint.Method, endpoint.FullMethod, endpoint.RPCType, endpoint.Handler, endpoint.HandlerType, endpoint.ServiceDesc, protoFile, endpoint.Package, registered, strings.Join(endpoint.Implementations, "\n"), endpoint.File, endpoint.Line); err != nil {
			return err
		}
	}
	return nil
}

func grpcShortNameFromService(service string) string {
	if service == "" {
		return ""
	}
	parts := strings.Split(service, ".")
	return parts[len(parts)-1]
}

func (s *WorkspaceStore) UpsertSymbolHashes(hashes []SymbolHash) error {
	if len(hashes) == 0 {
		return nil
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertSymbolHashesTx(tx, hashes)
	})
}

func (s *WorkspaceStore) GetStaleSymbolHashes(newHashes []SymbolHash) ([]SymbolHash, error) {
	if len(newHashes) == 0 {
		return nil, nil
	}
	stmt, err := s.DB.Prepare(`SELECT source_hash, embedding_version FROM symbol_hashes WHERE symbol_id = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return staleSymbolHashesFromStmt(stmt, newHashes)
}

type symbolHashQuery interface {
	QueryRow(args ...any) *sql.Row
}

func staleSymbolHashesFromStmt(stmt symbolHashQuery, newHashes []SymbolHash) ([]SymbolHash, error) {
	var stale []SymbolHash
	for _, next := range newHashes {
		var sourceHash string
		var embeddingVersion int
		err := stmt.QueryRow(next.SymbolID).Scan(&sourceHash, &embeddingVersion)
		if errors.Is(err, sql.ErrNoRows) || sourceHash != next.SourceHash || embeddingVersion != next.EmbeddingVersion {
			stale = append(stale, next)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return stale, nil
}

func (s *WorkspaceStore) UpsertSymbols(symbols []CodeSymbol) error {
	return s.UpsertSymbolsWithProvider(context.Background(), symbols, nil)
}

func (s *WorkspaceStore) UpsertSymbolsWithProvider(ctx context.Context, symbols []CodeSymbol, provider EmbeddingProvider) error {
	if len(symbols) == 0 {
		return nil
	}
	if provider != nil {
		texts := make([]string, len(symbols))
		for i, symbol := range symbols {
			texts[i] = symbol.Source
		}
		embeddings, err := provider.Embed(ctx, texts)
		if err != nil {
			return err
		}
		if len(embeddings) != len(symbols) {
			return fmt.Errorf("embedding provider returned %d vectors for %d symbols", len(embeddings), len(symbols))
		}
		for i := range symbols {
			symbols[i].Embedding = encodeEmbedding(embeddings[i])
		}
	}
	for i := range symbols {
		if len(symbols[i].Embedding) == 0 && symbols[i].Source != "" {
			symbols[i].Embedding = encodeEmbedding(deterministicEmbedding(symbols[i].Source, 16))
		}
	}
	if err := s.ensureVectorTableForSymbols(symbols); err != nil {
		return err
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		return s.upsertSymbolsTx(tx, symbols)
	})
}

func (s *WorkspaceStore) UpsertChangedSymbols(ctx context.Context, symbols []CodeSymbol, hashes []SymbolHash, provider EmbeddingProvider) error {
	if len(symbols) == 0 && len(hashes) == 0 {
		return nil
	}
	staleHashes, err := s.GetStaleSymbolHashes(hashes)
	if err != nil {
		return err
	}
	staleByID := make(map[string]bool, len(staleHashes))
	for _, hash := range staleHashes {
		staleByID[hash.SymbolID] = true
	}
	changed := make([]CodeSymbol, 0, len(staleByID))
	for _, symbol := range symbols {
		if staleByID[symbol.ID] {
			changed = append(changed, symbol)
		}
	}
	if provider != nil && len(changed) > 0 {
		if err := applySymbolEmbeddings(ctx, changed, provider); err != nil {
			return err
		}
	}
	for i := range changed {
		if len(changed[i].Embedding) == 0 && changed[i].Source != "" {
			changed[i].Embedding = encodeEmbedding(deterministicEmbedding(changed[i].Source, 16))
		}
	}
	if err := s.ensureVectorTableForSymbols(changed); err != nil {
		return err
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		if err := s.upsertSymbolsTx(tx, changed); err != nil {
			return err
		}
		if err := s.upsertSymbolHashesTx(tx, hashes); err != nil {
			return err
		}
		return nil
	})
}

func (s *WorkspaceStore) GetSymbolsByPackage(packagePath string) ([]CodeSymbol, error) {
	rows, err := s.DB.Query(`SELECT id, type, package_path, name, file_path, line_start, line_end, source, embedding
		FROM code_symbols WHERE package_path = ? ORDER BY id`, packagePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var symbols []CodeSymbol
	for rows.Next() {
		var symbol CodeSymbol
		if err := rows.Scan(&symbol.ID, &symbol.Type, &symbol.PackagePath, &symbol.Name, &symbol.FilePath, &symbol.LineStart, &symbol.LineEnd, &symbol.Source, &symbol.Embedding); err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	return symbols, rows.Err()
}

func (s *WorkspaceStore) SemanticSearch(query []float64, limit int) ([]CodeSymbol, error) {
	if len(query) == 0 {
		return nil, errors.New("semantic search query is required")
	}
	if limit <= 0 {
		limit = 10
	}
	if err := s.ensureVectorTable(len(query)); err != nil {
		return nil, err
	}
	queryVector, err := vectorJSON(query)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT cs.id, cs.type, cs.package_path, cs.name, cs.file_path, cs.line_start, cs.line_end, cs.source, cs.embedding
		FROM code_symbol_vec AS v
		JOIN code_symbol_vec_map AS m ON m.rowid = v.rowid
		JOIN code_symbols AS cs ON cs.id = m.symbol_id
		WHERE v.embedding MATCH ? AND v.k = ?
		ORDER BY v.distance
	`, queryVector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	symbols := make([]CodeSymbol, 0)
	for rows.Next() {
		var symbol CodeSymbol
		if err := rows.Scan(&symbol.ID, &symbol.Type, &symbol.PackagePath, &symbol.Name, &symbol.FilePath, &symbol.LineStart, &symbol.LineEnd, &symbol.Source, &symbol.Embedding); err != nil {
			return nil, err
		}
		symbols = append(symbols, symbol)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return symbols, nil
}

func ShadowStoreProgram(root string, prog *LoadedProgram) error {
	if prog == nil {
		return nil
	}
	store, err := OpenWorkspaceStore(root)
	if err != nil {
		return err
	}
	defer store.Close()

	edges := packageImportEdges(prog)
	config, err := EffectiveWorkspaceConfig(root)
	if err != nil {
		return err
	}
	provider, err := newEmbeddingProviderFromConfig(config.Embeddings)
	if err != nil {
		return err
	}
	hashes := withEmbeddingVersion(prog.symbolHashes, embeddingVersionForProvider(provider))
	changed, hashes, err := store.prepareChangedSymbols(context.Background(), prog.codeSymbols, hashes, provider)
	if err != nil {
		return err
	}
	if err := store.ensureVectorTableForSymbols(changed); err != nil {
		return err
	}
	return store.InTransaction(func(tx *sql.Tx) error {
		if err := store.upsertFileMetasTx(tx, prog.fileMetas); err != nil {
			return err
		}
		if err := store.upsertEdgesTx(tx, edges); err != nil {
			return err
		}
		if err := store.upsertMetricsTx(tx, prog.complexityMetrics); err != nil {
			return err
		}
		if err := store.upsertRoutesTx(tx, prog.httpRoutes); err != nil {
			return err
		}
		if err := store.upsertGRPCEndpointsTx(tx, prog.grpcEndpoints); err != nil {
			return err
		}
		if err := store.upsertGRPCRegistrationsTx(tx, prog.grpcRegistrations); err != nil {
			return err
		}
		if err := store.upsertSymbolsTx(tx, changed); err != nil {
			return err
		}
		if err := store.upsertSymbolHashesTx(tx, hashes); err != nil {
			return err
		}
		return nil
	})
}

func applySymbolEmbeddings(ctx context.Context, symbols []CodeSymbol, provider EmbeddingProvider) error {
	if provider == nil || len(symbols) == 0 {
		return nil
	}
	texts := make([]string, len(symbols))
	for i, symbol := range symbols {
		texts[i] = symbol.Source
	}
	embeddings, err := provider.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(embeddings) != len(symbols) {
		return fmt.Errorf("embedding provider returned %d vectors for %d symbols", len(embeddings), len(symbols))
	}
	for i := range symbols {
		symbols[i].Embedding = encodeEmbedding(embeddings[i])
	}
	return nil
}

func (s *WorkspaceStore) prepareChangedSymbols(ctx context.Context, symbols []CodeSymbol, hashes []SymbolHash, provider EmbeddingProvider) ([]CodeSymbol, []SymbolHash, error) {
	if len(symbols) == 0 && len(hashes) == 0 {
		return nil, nil, nil
	}
	hashes = withEmbeddingVersion(hashes, embeddingVersionForProvider(provider))
	staleHashes, err := s.GetStaleSymbolHashes(hashes)
	if err != nil {
		return nil, nil, err
	}
	staleByID := make(map[string]bool, len(staleHashes))
	for _, hash := range staleHashes {
		staleByID[hash.SymbolID] = true
	}
	changed := make([]CodeSymbol, 0, len(staleByID))
	for _, symbol := range symbols {
		if staleByID[symbol.ID] {
			changed = append(changed, symbol)
		}
	}
	if provider != nil && len(changed) > 0 {
		if err := applySymbolEmbeddings(ctx, changed, provider); err != nil {
			return nil, nil, err
		}
	}
	for i := range changed {
		if len(changed[i].Embedding) == 0 && changed[i].Source != "" {
			changed[i].Embedding = encodeEmbedding(deterministicEmbedding(changed[i].Source, 16))
		}
	}
	return changed, hashes, nil
}

func (s *WorkspaceStore) upsertFileMetasTx(tx *sql.Tx, metas []FileMeta) error {
	stmt, err := tx.Prepare(`INSERT INTO file_meta(path, hash, module, package, size, modified_at, indexed_at, last_build_ssa_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash,
			module=excluded.module,
			package=excluded.package,
			size=excluded.size,
			modified_at=excluded.modified_at,
			indexed_at=excluded.indexed_at,
			last_build_ssa_at=excluded.last_build_ssa_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, meta := range metas {
		if _, err := stmt.Exec(meta.Path, meta.Hash, meta.Module, meta.Package, meta.Size, meta.ModifiedAt, meta.IndexedAt, nullableTime(meta.LastBuildSSAAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) upsertSymbolsTx(tx *sql.Tx, symbols []CodeSymbol) error {
	if len(symbols) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO code_symbols(id, type, package_path, name, file_path, line_start, line_end, source, embedding)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type=excluded.type,
			package_path=excluded.package_path,
			name=excluded.name,
			file_path=excluded.file_path,
			line_start=excluded.line_start,
			line_end=excluded.line_end,
			source=excluded.source,
			embedding=excluded.embedding`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, symbol := range symbols {
		embedding := symbol.Embedding
		if len(embedding) == 0 && symbol.Source != "" {
			embedding = encodeEmbedding(deterministicEmbedding(symbol.Source, 16))
		}
		if _, err := stmt.Exec(symbol.ID, symbol.Type, symbol.PackagePath, symbol.Name, symbol.FilePath, symbol.LineStart, symbol.LineEnd, symbol.Source, embedding); err != nil {
			return err
		}
		if err := s.upsertSymbolVectorTx(tx, symbol.ID, embedding); err != nil {
			return err
		}
	}
	return nil
}

func (s *WorkspaceStore) upsertSymbolVectorTx(tx *sql.Tx, symbolID string, embedding []byte) error {
	vec := decodeEmbedding(embedding)
	if len(vec) == 0 {
		return nil
	}
	rowID, err := s.vectorRowIDTx(tx, symbolID)
	if err != nil {
		return err
	}
	vector, err := vectorJSON(vec)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM code_symbol_vec WHERE rowid = ?`, rowID); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO code_symbol_vec(rowid, embedding) VALUES(?, ?)`, rowID, vector)
	return err
}

func (s *WorkspaceStore) vectorRowIDTx(tx *sql.Tx, symbolID string) (int64, error) {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO code_symbol_vec_map(symbol_id) VALUES(?)`, symbolID); err != nil {
		return 0, err
	}
	var rowID int64
	if err := tx.QueryRow(`SELECT rowid FROM code_symbol_vec_map WHERE symbol_id = ?`, symbolID).Scan(&rowID); err != nil {
		return 0, err
	}
	return rowID, nil
}

func (s *WorkspaceStore) ensureVectorTable(dimension int) error {
	if dimension <= 0 {
		return errors.New("vector dimension must be positive")
	}
	var current sql.NullString
	err := s.DB.QueryRow(`SELECT value FROM system_state WHERE key = 'code_symbol_vec_dimension'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current.Valid {
		currentDimension, err := strconv.Atoi(current.String)
		if err != nil {
			return err
		}
		if currentDimension == dimension {
			if _, err := s.DB.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(embedding float[%d])`, codeSymbolVectorTable, dimension)); err != nil {
				return err
			}
			return nil
		}
		if _, err := s.DB.Exec(`DROP TABLE IF EXISTS code_symbol_vec`); err != nil {
			return err
		}
	}
	if _, err := s.DB.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(embedding float[%d])`, codeSymbolVectorTable, dimension)); err != nil {
		return err
	}
	if _, err := s.DB.Exec(`INSERT INTO system_state(key, value, updated_at)
		VALUES('code_symbol_vec_dimension', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		strconv.Itoa(dimension), time.Now().UTC()); err != nil {
		return err
	}
	return s.rebuildVectorTable(dimension)
}

func (s *WorkspaceStore) ensureVectorTableForSymbols(symbols []CodeSymbol) error {
	dimension := 0
	for _, symbol := range symbols {
		if vec := decodeEmbedding(symbol.Embedding); len(vec) > 0 {
			dimension = len(vec)
			break
		}
		if symbol.Source != "" {
			dimension = 16
			break
		}
	}
	if dimension == 0 {
		return nil
	}
	return s.ensureVectorTable(dimension)
}

func (s *WorkspaceStore) rebuildVectorTable(dimension int) error {
	rows, err := s.DB.Query(`SELECT id, embedding FROM code_symbols WHERE embedding IS NOT NULL`)
	if err != nil {
		return err
	}
	type row struct {
		id        string
		embedding []byte
	}
	var symbols []row
	for rows.Next() {
		var symbol row
		if err := rows.Scan(&symbol.id, &symbol.embedding); err != nil {
			_ = rows.Close()
			return err
		}
		symbols = append(symbols, symbol)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.InTransaction(func(tx *sql.Tx) error {
		for _, symbol := range symbols {
			vec := decodeEmbedding(symbol.embedding)
			if len(vec) != dimension {
				continue
			}
			rowID, err := s.vectorRowIDTx(tx, symbol.id)
			if err != nil {
				return err
			}
			vector, err := vectorJSON(vec)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO code_symbol_vec(rowid, embedding) VALUES(?, ?)`, rowID, vector); err != nil {
				return err
			}
		}
		return nil
	})
}

func vectorJSON(vec []float64) (string, error) {
	data, err := json.Marshal(vec)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *WorkspaceStore) upsertSymbolHashesTx(tx *sql.Tx, hashes []SymbolHash) error {
	if len(hashes) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO symbol_hashes(symbol_id, file_path, symbol_type, symbol_name, source_hash, embedding_version, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol_id) DO UPDATE SET
			file_path=excluded.file_path,
			symbol_type=excluded.symbol_type,
			symbol_name=excluded.symbol_name,
			source_hash=excluded.source_hash,
			embedding_version=excluded.embedding_version`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, hash := range hashes {
		if hash.CreatedAt.IsZero() {
			hash.CreatedAt = time.Now().UTC()
		}
		if _, err := stmt.Exec(hash.SymbolID, hash.FilePath, hash.SymbolType, hash.SymbolName, hash.SourceHash, hash.EmbeddingVersion, hash.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func withEmbeddingVersion(hashes []SymbolHash, version int) []SymbolHash {
	if len(hashes) == 0 {
		return nil
	}
	out := make([]SymbolHash, len(hashes))
	for i, hash := range hashes {
		hash.EmbeddingVersion = version
		out[i] = hash
	}
	return out
}

func packageImportEdges(prog *LoadedProgram) []ArchEdge {
	if prog == nil {
		return nil
	}
	rootPaths := make(map[string]bool, len(prog.Packages))
	for _, pkg := range prog.Packages {
		if pkg != nil && pkg.PkgPath != "" {
			rootPaths[pkg.PkgPath] = true
		}
	}

	edges := make([]ArchEdge, 0)
	now := time.Now().UTC()
	for _, pkg := range prog.Packages {
		if pkg == nil || pkg.PkgPath == "" {
			continue
		}
		seen := make(map[string]bool, len(pkg.Imports))
		for importPath, imp := range pkg.Imports {
			targetPath := strings.TrimSpace(importPath)
			if targetPath == "" && imp != nil {
				targetPath = strings.TrimSpace(imp.PkgPath)
			}
			if targetPath == "" {
				continue
			}
			if seen[targetPath] {
				continue
			}
			seen[targetPath] = true
			targetType := "external_package"
			if rootPaths[targetPath] {
				targetType = "package"
			}
			targetName := targetPath
			if imp != nil && strings.TrimSpace(imp.Name) != "" {
				targetName = strings.TrimSpace(imp.Name)
			}
			edges = append(edges, ArchEdge{
				SourceType: "package",
				SourcePath: pkg.PkgPath,
				SourceName: pkg.Name,
				TargetType: targetType,
				TargetPath: targetPath,
				TargetName: targetName,
				EdgeType:   "imports",
				CreatedAt:  now,
			})
		}
	}
	return edges
}

func scanFileMeta(scanner interface {
	Scan(dest ...any) error
}) (FileMeta, error) {
	var meta FileMeta
	var lastBuild sql.NullTime
	err := scanner.Scan(&meta.Path, &meta.Hash, &meta.Module, &meta.Package, &meta.Size, &meta.ModifiedAt, &meta.IndexedAt, &lastBuild)
	if lastBuild.Valid {
		meta.LastBuildSSAAt = lastBuild.Time
	}
	return meta, err
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
