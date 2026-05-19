package analyzer

import (
	"errors"
	"testing"
)

func TestComputeMutex_PreemptsLowerPriorityHolder(t *testing.T) {
	mutex := NewComputeMutex()
	locked, err := mutex.TryLock("background", PriorityNormal)
	if err != nil || !locked {
		t.Fatalf("expected background lock, locked=%v err=%v", locked, err)
	}

	locked, err = mutex.TryLock("tool", PriorityHigh)
	if locked {
		t.Fatal("high-priority preemption should signal abort, not share the lock")
	}
	var aborted ErrComputeAborted
	if !errors.As(err, &aborted) {
		t.Fatalf("expected ErrComputeAborted, got %v", err)
	}
	if !mutex.CheckAborted() {
		t.Fatal("expected background holder to see abort signal")
	}

	mutex.Unlock("background")
	locked, err = mutex.TryLock("tool", PriorityHigh)
	if err != nil || !locked {
		t.Fatalf("expected high-priority lock after background unlock, locked=%v err=%v", locked, err)
	}
}

func TestComputeMutex_BlocksPeerPriority(t *testing.T) {
	mutex := NewComputeMutex()
	locked, err := mutex.TryLock("one", PriorityNormal)
	if err != nil || !locked {
		t.Fatalf("expected first lock, locked=%v err=%v", locked, err)
	}
	locked, err = mutex.TryLock("two", PriorityNormal)
	if locked {
		t.Fatal("same-priority contender should not get lock")
	}
	var busy ErrComputeInProgress
	if !errors.As(err, &busy) {
		t.Fatalf("expected ErrComputeInProgress, got %v", err)
	}
}
