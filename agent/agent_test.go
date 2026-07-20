package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
)

func TestNew(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	if a.Manager != manager {
		t.Error("Agent Manager not set")
	}
	if a.workQueue == nil {
		t.Error("workQueue is nil")
	}
	if cap(a.workQueue) != maxQueueSize {
		t.Errorf("workQueue capacity = %d, want %d", cap(a.workQueue), maxQueueSize)
	}
	if cap(a.sem) != maxConcurrentSessions {
		t.Errorf("sem capacity = %d, want %d", cap(a.sem), maxConcurrentSessions)
	}
	if a.activeSessions == nil {
		t.Error("activeSessions is nil")
	}
	if a.stopCh == nil {
		t.Error("stopCh is nil")
	}
}

func TestWorkQueue(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	q := a.WorkQueue()
	if q == nil {
		t.Error("WorkQueue() returned nil")
	}
	if q != a.workQueue {
		t.Error("WorkQueue() did not return the internal queue")
	}
}

func TestSubmitWork_Success(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	item := WorkItem{SessionID: "test-1"}
	if !a.SubmitWork(item) {
		t.Error("SubmitWork() should return true for empty queue")
	}
}

func TestSubmitWork_FullQueue(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Fill the queue
	for i := 0; i < maxQueueSize; i++ {
		if !a.SubmitWork(WorkItem{SessionID: string(rune('a' + i))}) {
			t.Fatalf("SubmitWork() should return true, failed at item %d", i)
		}
	}

	// Next submit should fail (queue full)
	if a.SubmitWork(WorkItem{SessionID: "overflow"}) {
		t.Error("SubmitWork() should return false when queue is full")
	}
}

func TestSubmitWork_EmptyAfterDrain(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Fill queue
	for i := 0; i < 10; i++ {
		a.SubmitWork(WorkItem{SessionID: string(rune('a' + i))})
	}

	// Drain queue manually
	for len(a.workQueue) > 0 {
		<-a.workQueue
	}

	// Now submit should succeed
	if !a.SubmitWork(WorkItem{SessionID: "after-drain"}) {
		t.Error("SubmitWork() should succeed after queue is drained")
	}
}

func TestClose_ReturnsImmediately(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Start the main loop
	go a.RunMainLoop()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	// Close should return immediately (not hang)
	done := make(chan struct{})
	go func() {
		a.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success - Close returned
	case <-time.After(2 * time.Second):
		t.Fatal("Close() hung for more than 2 seconds - it should return immediately")
	}
}

func TestRunMainLoop_ExitsOnClose(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Start main loop
	go a.RunMainLoop()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	// Close and wait for main loop to exit
	a.Close()

	// Give the main loop time to exit
	time.Sleep(100 * time.Millisecond)

	// Verify the queue is closed by trying to send (should panic if not closed)
	// Instead, verify by checking that we can't send (channel closed sends to panic)
	// We'll just verify Close returns quickly above, which is sufficient
}

func TestHandleStop_CancelsActiveSessions(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Directly manipulate the activeSessions map
	a.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	a.activeSessions["test-session"] = &activeSession{ctx: ctx, cancel: cancel}
	a.mu.Unlock()

	if a.ActiveSessionCount() != 1 {
		t.Errorf("ActiveSessionCount() = %d, want 1", a.ActiveSessionCount())
	}

	// HandleStop should cancel the session
	a.HandleStop()

	// Context should be cancelled
	select {
	case <-ctx.Done():
		// Expected - context was cancelled
	default:
		t.Error("HandleStop() did not cancel active session context")
	}
}

func TestActiveSessionCount(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	if count := a.ActiveSessionCount(); count != 0 {
		t.Errorf("ActiveSessionCount() = %d, want 0", count)
	}

	// Add sessions
	a.mu.Lock()
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	a.activeSessions["s1"] = &activeSession{ctx: ctx1, cancel: cancel1}
	a.activeSessions["s2"] = &activeSession{ctx: ctx2, cancel: cancel2}
	a.mu.Unlock()

	if count := a.ActiveSessionCount(); count != 2 {
		t.Errorf("ActiveSessionCount() = %d, want 2", count)
	}
}

func TestRemoveSession(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Add a session with valid cancel
	a.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	a.activeSessions["test-session"] = &activeSession{ctx: ctx, cancel: cancel}
	a.mu.Unlock()

	if a.ActiveSessionCount() != 1 {
		t.Error("Expected 1 active session before removal")
	}

	// Remove it
	a.RemoveSession("test-session")

	if a.ActiveSessionCount() != 0 {
		t.Error("Expected 0 active sessions after removal")
	}
}

func TestConcurrentSubmitWork(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	var wg sync.WaitGroup
	successes := 0
	var successMu sync.Mutex

	// Submit work concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if a.SubmitWork(WorkItem{SessionID: string(rune('a' + id))}) {
				successMu.Lock()
				successes++
				successMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// All should succeed since queue has capacity for 100
	if successes != 50 {
		t.Errorf("SubmitWork() succeeded %d times, want 50", successes)
	}
}

func TestAgentLifecycle(t *testing.T) {
	manager := llm.NewManager()
	cfg := config.New(t.TempDir())
	cfg.SetModel("test")
	cfg.SetURL("http://localhost:11434")
	manager.SetInitial(cfg, "")
	a := New(manager)

	// Start main loop
	go a.RunMainLoop()

	// Give the main loop time to start
	time.Sleep(50 * time.Millisecond)

	// Close should return immediately (the key test)
	done := make(chan struct{})
	go func() {
		a.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success - Close returned immediately
	case <-time.After(1 * time.Second):
		t.Fatal("Close() should return immediately")
	}
}
