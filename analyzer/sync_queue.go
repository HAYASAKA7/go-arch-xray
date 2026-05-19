package analyzer

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type RebuildPriority int

const (
	PriorityHigh RebuildPriority = iota
	PriorityNormal
	PriorityLow
)

type RebuildRequest struct {
	rootPath  string
	patterns  []string
	priority  RebuildPriority
	forceFull bool
	reason    string
	createdBy string
	createdAt time.Time
}

type QueueStatus struct {
	Pending   int             `json:"pending"`
	Active    *RebuildRequest `json:"active,omitempty"`
	Aborted   bool            `json:"aborted"`
	Reason    string          `json:"reason,omitempty"`
	LastError string          `json:"last_error,omitempty"`
}

type RebuildQueue struct {
	pending   []RebuildRequest
	active    *RebuildRequest
	aborted   bool
	reason    string
	lastError string
	mu        sync.Mutex
	cv        *sync.Cond
}

func NewRebuildQueue() *RebuildQueue {
	q := &RebuildQueue{}
	q.cv = sync.NewCond(&q.mu)
	return q
}

func (q *RebuildQueue) Enqueue(req RebuildRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if req.createdAt.IsZero() {
		req.createdAt = time.Now().UTC()
	}
	q.pending = append(q.pending, req)
	sort.SliceStable(q.pending, func(i, j int) bool {
		if q.pending[i].priority != q.pending[j].priority {
			return q.pending[i].priority < q.pending[j].priority
		}
		return q.pending[i].createdAt.Before(q.pending[j].createdAt)
	})
	q.cv.Broadcast()
}

func (q *RebuildQueue) Dequeue(ctx context.Context) (RebuildRequest, error) {
	for {
		q.mu.Lock()
		if len(q.pending) > 0 {
			req := q.pending[0]
			q.pending = q.pending[1:]
			q.active = &req
			q.aborted = false
			q.reason = ""
			q.mu.Unlock()
			return req, nil
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return RebuildRequest{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (q *RebuildQueue) Abort(reason string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.aborted = true
	q.reason = reason
	q.cv.Broadcast()
}

func (q *RebuildQueue) Status() QueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	var active *RebuildRequest
	if q.active != nil {
		copyReq := *q.active
		active = &copyReq
	}
	return QueueStatus{
		Pending:   len(q.pending),
		Active:    active,
		Aborted:   q.aborted,
		Reason:    q.reason,
		LastError: q.lastError,
	}
}

func (q *RebuildQueue) setError(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err != nil {
		q.lastError = err.Error()
	}
}

func (q *RebuildQueue) finishActive() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active = nil
	q.aborted = false
	q.reason = ""
}

func (q *RebuildQueue) ErrNoWork() error {
	return errors.New("no rebuild work available")
}
