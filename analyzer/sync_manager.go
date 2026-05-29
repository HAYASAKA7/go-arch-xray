package analyzer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SyncManager struct {
	status    SyncStatus
	mu        sync.RWMutex
	workspace *Workspace
	queue     *RebuildQueue
	builder   *SSABuilder
	mutex     *ComputeMutex
	statePath string
	rootPath  string
}

type SSABuilder struct {
	workspace *Workspace
	store     *WorkspaceStore
	rootPath  string
	patterns  []string
	mutex     *ComputeMutex
}

func NewSSABuilder(workspace *Workspace, store *WorkspaceStore, rootPath string, patterns []string) *SSABuilder {
	return &SSABuilder{
		workspace: workspace,
		store:     store,
		rootPath:  filepath.Clean(rootPath),
		patterns:  append([]string(nil), patterns...),
		mutex:     NewComputeMutex(),
	}
}

func NewSyncManager(workspace *Workspace, statePath string) *SyncManager {
	return NewSyncManagerWithRoot(workspace, "", statePath)
}

func NewSyncManagerWithRoot(workspace *Workspace, rootPath, statePath string) *SyncManager {
	return NewSyncManagerWithQueueAndRoot(workspace, NewRebuildQueue(), rootPath, statePath)
}

func NewSyncManagerWithQueue(workspace *Workspace, statePath string, queue *RebuildQueue) *SyncManager {
	return NewSyncManagerWithQueueAndRoot(workspace, queue, "", statePath)
}

func NewSyncManagerWithQueueAndRoot(workspace *Workspace, queue *RebuildQueue, rootPath, statePath string) *SyncManager {
	if queue == nil {
		queue = NewRebuildQueue()
	}
	if rootPath != "" {
		rootPath = filepath.Clean(rootPath)
	}
	return &SyncManager{
		status:    SyncStatus{State: StateIdle},
		workspace: workspace,
		queue:     queue,
		builder:   NewSSABuilder(workspace, nil, rootPath, []string{"./..."}),
		mutex:     NewComputeMutex(),
		statePath: statePath,
		rootPath:  rootPath,
	}
}

func (m *SyncManager) Mutex() *ComputeMutex {
	if m == nil {
		return nil
	}
	return m.mutex
}

func (m *SyncManager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	if m.builder == nil {
		m.builder = NewSSABuilder(m.workspace, nil, m.rootPath, []string{"./..."})
	}
	go m.backgroundBuildLoop(ctx)
}

func (m *SyncManager) Queue() *RebuildQueue {
	if m == nil {
		return nil
	}
	return m.queue
}

func (m *SyncManager) RootPath() string {
	if m == nil {
		return ""
	}
	return m.rootPath
}

func (m *SyncManager) Trigger(reason string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.status.State = StateComputing
	m.status.Reason = reason
	m.status.StartedAt = time.Now().UTC()
	m.mu.Unlock()

	root := m.rootPath
	if root == "" {
		root = defaultWorkspaceRootPath()
	}
	m.queue.Enqueue(RebuildRequest{
		rootPath:  root,
		patterns:  nil,
		priority:  PriorityHigh,
		reason:    reason,
		createdBy: "tool",
	})
	return nil
}

func (m *SyncManager) GetStatus() SyncStatus {
	if m == nil {
		return SyncStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *SyncManager) Pause() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.State = StatePaused
}

func (m *SyncManager) Resume() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.State = StateIdle
}

func (m *SyncManager) setError(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.State = StateError
	if err != nil {
		m.queue.setError(err)
		m.status.Reason = err.Error()
	}
}

func (m *SyncManager) backgroundBuildLoop(ctx context.Context) {
	if m == nil {
		return
	}
	for {
		req, err := m.queue.Dequeue(ctx)
		if err != nil {
			return
		}
		m.mu.Lock()
		m.status.State = StateComputing
		m.status.Reason = req.reason
		m.status.StartedAt = time.Now().UTC()
		m.mu.Unlock()

		owner := "sync-manager"
		locked, err := m.mutex.TryLock(owner, req.priority)
		if err != nil {
			var aborted ErrComputeAborted
			if errors.As(err, &aborted) {
				m.queue.Enqueue(req)
				m.queue.finishActive()
				continue
			}
			var busy ErrComputeInProgress
			if errors.As(err, &busy) {
				m.queue.Enqueue(req)
				m.queue.finishActive()
				time.Sleep(50 * time.Millisecond)
				continue
			}
			m.setError(err)
			m.queue.finishActive()
			continue
		}
		if !locked {
			m.queue.Enqueue(req)
			m.queue.finishActive()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if m.mutex.CheckAborted() {
			m.mutex.Unlock(owner)
			m.queue.Enqueue(req)
			m.queue.finishActive()
			continue
		}
		if err := m.runRebuild(ctx, req); err != nil {
			m.mutex.Unlock(owner)
			m.setError(err)
			m.queue.finishActive()
			continue
		}
		m.mutex.Unlock(owner)
		m.queue.finishActive()
		m.mu.Lock()
		m.status.State = StateIdle
		m.status.Progress = 1
		m.mu.Unlock()
	}
}

func (m *SyncManager) runRebuild(ctx context.Context, req RebuildRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root := req.rootPath
	if root == "" && m.workspace != nil {
		// Root is required for a real rebuild. Empty requests are status-only
		// triggers until a watcher/tool supplies a root path.
		return nil
	}
	pattern := "./..."
	if len(req.patterns) > 0 {
		pattern = strings.Join(req.patterns, ",")
	}
	if m.workspace != nil {
		if req.forceFull {
			m.workspace.Invalidate(root, pattern)
		}
		if req.rootPath != "" {
			if _, err := m.workspace.GetOrLoad(req.rootPath, pattern); err != nil {
				return err
			}
		}
	}
	if m.builder == nil {
		m.builder = NewSSABuilder(m.workspace, nil, root, strings.Split(pattern, ","))
	}
	m.builder.workspace = m.workspace
	m.builder.rootPath = root
	m.builder.patterns = strings.Split(pattern, ",")
	if m.builder.store == nil {
		if store, err := OpenWorkspaceStore(root); err == nil {
			m.builder.store = store
			defer store.Close()
		}
	}
	if err := m.builder.ComputeAndSnapshot(ctx); err != nil {
		var inProgress ErrComputeInProgress
		if errors.As(err, &inProgress) {
			return err
		}
		return fmt.Errorf("compute snapshot: %w", err)
	}
	hashes, err := scanHashes(root, nil)
	if err != nil {
		return err
	}
	files := make(map[string]FileHash, len(hashes))
	for path, hash := range hashes {
		info, statErr := os.Stat(filepath.FromSlash(path))
		fileHash := FileHash{Hash: hash}
		if statErr == nil {
			fileHash.Size = info.Size()
			fileHash.ModifiedAt = info.ModTime().UTC()
		}
		files[path] = fileHash
	}
	status := m.GetStatus()
	status.State = StateIdle
	status.Progress = 1
	status.FilesTotal = len(files)
	status.FilesDone = len(files)
	state := &PersistedState{
		Version:  1,
		LastSync: time.Now().UTC(),
		Files:    files,
		Status:   status,
	}
	state.LastHash = aggregateFileHashes(hashes)
	if m.statePath != "" {
		if err := state.Save(m.statePath); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
	return nil
}

func (b *SSABuilder) ComputeAndSnapshot(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if b.mutex == nil {
		b.mutex = NewComputeMutex()
	}
	locked, err := b.mutex.TryLock("ssa-builder", PriorityHigh)
	if err != nil {
		var busy ErrComputeInProgress
		if errors.As(err, &busy) {
			return err
		}
		var aborted ErrComputeAborted
		if errors.As(err, &aborted) {
			return err
		}
		return err
	}
	if !locked {
		return ErrComputeInProgress{ETA: b.mutex.Status().ETA}
	}
	defer b.mutex.Unlock("ssa-builder")

	if err := ctx.Err(); err != nil {
		return err
	}
	if b.workspace == nil || b.store == nil {
		return fmt.Errorf("compute snapshot requires workspace and store")
	}
	root := b.rootPath
	if root == "" {
		root = defaultWorkspaceRootPath()
	}
	patterns := append([]string(nil), b.patterns...)
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	pattern := strings.Join(patterns, ",")
	prog, err := b.workspace.GetOrLoadSSA(root, pattern)
	if err != nil {
		return err
	}
	config, err := EffectiveWorkspaceConfig(root)
	if err != nil {
		return err
	}
	provider, err := newEmbeddingProviderFromConfig(config.Embeddings)
	if err != nil {
		return err
	}
	changed, hashes, err := b.store.prepareChangedSymbols(ctx, prog.codeSymbols, prog.symbolHashes, provider)
	if err != nil {
		return err
	}
	if err := b.store.ensureVectorTableForSymbols(changed); err != nil {
		return err
	}
	return b.store.InTransaction(func(tx *sql.Tx) error {
		if err := b.store.upsertFileMetasTx(tx, prog.fileMetas); err != nil {
			return err
		}
		if err := b.store.upsertEdgesTx(tx, packageImportEdges(prog)); err != nil {
			return err
		}
		if err := b.store.upsertMetricsTx(tx, prog.complexityMetrics); err != nil {
			return err
		}
		if err := b.store.upsertRoutesTx(tx, prog.httpRoutes); err != nil {
			return err
		}
		if err := b.store.upsertGRPCEndpointsTx(tx, prog.grpcEndpoints); err != nil {
			return err
		}
		if err := b.store.upsertGRPCRegistrationsTx(tx, prog.grpcRegistrations); err != nil {
			return err
		}
		if err := b.store.upsertSymbolsTx(tx, changed); err != nil {
			return err
		}
		if err := b.store.upsertSymbolHashesTx(tx, hashes); err != nil {
			return err
		}
		return nil
	})
}

func defaultWorkspaceRootPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
