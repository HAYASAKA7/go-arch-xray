package analyzer

import (
	"fmt"
	"sync"
	"time"
)

type ErrComputeInProgress struct {
	ETA time.Duration
}

func (e ErrComputeInProgress) Error() string {
	return fmt.Sprintf("compute in progress; retry in %s", e.ETA)
}

type ErrComputeAborted struct {
	Reason string
}

func (e ErrComputeAborted) Error() string {
	if e.Reason == "" {
		return "compute aborted"
	}
	return "compute aborted: " + e.Reason
}

type ComputeMutexStatus struct {
	Locked   bool
	Owner    string
	Priority RebuildPriority
	Aborted  bool
	ETA      time.Duration
}

type ComputeMutex struct {
	mu       sync.Mutex
	holder   string
	priority RebuildPriority
	aborted  bool
	eta      time.Duration
}

func NewComputeMutex() *ComputeMutex {
	return &ComputeMutex{priority: PriorityLow, eta: 5 * time.Second}
}

func (m *ComputeMutex) TryLock(owner string, priority RebuildPriority) (bool, error) {
	if m == nil {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.holder == "" {
		m.holder = owner
		m.priority = priority
		m.aborted = false
		return true, nil
	}
	if m.holder == owner {
		return true, nil
	}
	if priority == PriorityHigh && m.priority > PriorityHigh {
		m.aborted = true
		return false, ErrComputeAborted{Reason: "preempted by high-priority request"}
	}
	return false, ErrComputeInProgress{ETA: m.eta}
}

func (m *ComputeMutex) Unlock(owner string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner != "" && m.holder != "" && m.holder != owner {
		return
	}
	m.holder = ""
	m.priority = PriorityLow
	m.aborted = false
}

func (m *ComputeMutex) CheckAborted() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aborted
}

func (m *ComputeMutex) Status() ComputeMutexStatus {
	if m == nil {
		return ComputeMutexStatus{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return ComputeMutexStatus{
		Locked:   m.holder != "",
		Owner:    m.holder,
		Priority: m.priority,
		Aborted:  m.aborted,
		ETA:      m.eta,
	}
}
