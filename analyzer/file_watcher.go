package analyzer

import (
	"context"
	"time"
)

type FileWatcher struct {
	root     string
	db       *WorkspaceStore
	debounce time.Duration
	changes  chan string
	done     chan struct{}
	queue    *RebuildQueue
	checker  *HashChecker
}

func NewFileWatcher(root string, db *WorkspaceStore, queue *RebuildQueue) *FileWatcher {
	return &FileWatcher{
		root:     root,
		db:       db,
		debounce: 2500 * time.Millisecond,
		changes:  make(chan string, 128),
		done:     make(chan struct{}),
		queue:    queue,
		checker:  NewHashChecker(db),
	}
}

func (w *FileWatcher) Start(ctx context.Context) error {
	go w.debouncedProcess(ctx)
	return nil
}

func (w *FileWatcher) SetDebounce(debounce time.Duration) {
	if w == nil || debounce <= 0 {
		return
	}
	w.debounce = debounce
}

func (w *FileWatcher) StartPolling(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := w.Start(ctx); err != nil {
		return err
	}
	go w.poll(ctx, interval)
	return nil
}

func (w *FileWatcher) Stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

func (w *FileWatcher) debouncedProcess(ctx context.Context) {
	ticker := time.NewTicker(w.debounce)
	defer ticker.Stop()
	pending := make(map[string]struct{})
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case path := <-w.changes:
			if path != "" {
				pending[path] = struct{}{}
			}
		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}
			w.queue.Enqueue(RebuildRequest{
				rootPath:  w.root,
				priority:  PriorityNormal,
				reason:    "file watcher detected changes",
				createdBy: "watcher",
				createdAt: time.Now().UTC(),
			})
			pending = make(map[string]struct{})
		}
	}
}

func (w *FileWatcher) Trigger(path string) {
	select {
	case w.changes <- path:
	default:
	}
}

func (w *FileWatcher) poll(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			_ = w.ScanAndEnqueue(ctx)
		}
	}
}

func (w *FileWatcher) ScanAndEnqueue(ctx context.Context) error {
	if w == nil || w.checker == nil {
		return nil
	}
	current, err := w.checker.Scan(w.root)
	if err != nil {
		return err
	}
	changed, deleted, err := w.checker.DetectChanges(current)
	if err != nil {
		return err
	}
	for _, path := range changed {
		w.Trigger(path)
	}
	for _, path := range deleted {
		w.Trigger(path)
	}
	return nil
}
