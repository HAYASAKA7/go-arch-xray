package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileHash struct {
	Hash       string    `json:"hash"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

type SyncState int

const (
	StateIdle SyncState = iota
	StateComputing
	StateWriting
	StateError
	StatePaused
)

type SyncStatus struct {
	State      SyncState     `json:"state"`
	Progress   float64       `json:"progress"`
	FilesTotal int           `json:"files_total"`
	FilesDone  int           `json:"files_done"`
	ETA        time.Duration `json:"eta"`
	StartedAt  time.Time     `json:"started_at"`
	Reason     string        `json:"reason"`
}

type PersistedState struct {
	Version  int                 `json:"version"`
	LastSync time.Time           `json:"last_sync"`
	LastHash string              `json:"last_hash"`
	Files    map[string]FileHash `json:"files"`
	Status   SyncStatus          `json:"status"`
}

type UserWorkspaceState struct {
	Version    int                 `json:"version"`
	Embeddings UserEmbeddingsState `json:"embeddings,omitempty"`
}

type UserEmbeddingsState struct {
	Dismissed bool      `json:"dismissed"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func LoadState(path string) (*PersistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &PersistedState{Version: 1, Files: make(map[string]FileHash)}, nil
		}
		return nil, fmt.Errorf("read sync state: %w", err)
	}
	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse sync state: %w", err)
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Files == nil {
		state.Files = make(map[string]FileHash)
	}
	return &state, nil
}

func (s *PersistedState) Save(path string) error {
	if s == nil {
		return fmt.Errorf("sync state is nil")
	}
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Files == nil {
		s.Files = make(map[string]FileHash)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sync state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create sync state dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write sync state: %w", err)
	}
	return nil
}

func LoadUserWorkspaceState() (*UserWorkspaceState, error) {
	path := UserWorkspaceStatePath()
	if path == "" {
		return &UserWorkspaceState{Version: 1}, nil
	}
	return LoadUserWorkspaceStateFromPath(path)
}

func LoadUserWorkspaceStateFromPath(path string) (*UserWorkspaceState, error) {
	if path == "" {
		return &UserWorkspaceState{Version: 1}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserWorkspaceState{Version: 1}, nil
		}
		return nil, fmt.Errorf("read user workspace state: %w", err)
	}
	var state UserWorkspaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse user workspace state: %w", err)
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return &state, nil
}

func SaveUserWorkspaceState(state *UserWorkspaceState) error {
	path := UserWorkspaceStatePath()
	if path == "" {
		return fmt.Errorf("user workspace state path is not configured")
	}
	return SaveUserWorkspaceStateToPath(path, state)
}

func SaveUserWorkspaceStateToPath(path string, state *UserWorkspaceState) error {
	if path == "" {
		return fmt.Errorf("user workspace state path is not configured")
	}
	if state == nil {
		return fmt.Errorf("user workspace state is nil")
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create user workspace state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal user workspace state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write user workspace state: %w", err)
	}
	return nil
}

func UpdateUserWorkspaceEmbeddingsState(dismissed bool) (*UserWorkspaceState, error) {
	state, err := LoadUserWorkspaceState()
	if err != nil {
		return nil, err
	}
	state.Embeddings.Dismissed = dismissed
	state.Embeddings.UpdatedAt = time.Now().UTC()
	if err := SaveUserWorkspaceState(state); err != nil {
		return nil, err
	}
	return state, nil
}

type HashChecker struct {
	db *WorkspaceStore
}

func NewHashChecker(db *WorkspaceStore) *HashChecker {
	return &HashChecker{db: db}
}

func (h *HashChecker) Scan(dir string) (map[string]string, error) {
	return scanHashes(dir, nil)
}

func (h *HashChecker) DetectChanges(current map[string]string) ([]string, []string, error) {
	if h == nil || h.db == nil {
		return nil, nil, fmt.Errorf("hash checker database is not configured")
	}
	var changed []string
	var deleted []string
	for path, hash := range current {
		meta, err := h.db.GetFileByPath(path)
		if err != nil {
			if errorsIsNoRows(err) {
				changed = append(changed, path)
				continue
			}
			return nil, nil, err
		}
		if meta.Hash != hash {
			changed = append(changed, path)
		}
	}
	existing, err := h.db.GetFilesByPrefix("")
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]bool, len(current))
	for path := range current {
		seen[path] = true
	}
	for _, file := range existing {
		if !seen[file.Path] {
			deleted = append(deleted, file.Path)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted, nil
}

func errorsIsNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows in result set")
}

func scanHashes(dir string, filter *SourceFilter) (map[string]string, error) {
	out := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".gax" || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		if filter != nil && !filter.ShouldProcess(rel) {
			return nil
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			absPath = path
		}
		out[filepath.ToSlash(absPath)] = sum
		return nil
	})
	return out, err
}

func aggregateFileHashes(hashes map[string]string) string {
	if len(hashes) == 0 {
		return ""
	}
	keys := make([]string, 0, len(hashes))
	for key := range hashes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(hashes[key]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
