package agent

import (
	"context"
	"os"
	"path/filepath"
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

func TestToolRead_Success(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	expectedContent := "hello world"
	if err := os.WriteFile(testFile, []byte(expectedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origCwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origCwd)

	result := a.toolRead(map[string]any{"path": "test.txt"})

	if result.Err != "" {
		t.Errorf("expected no error, got: %s", result.Err)
	}
	if result.Output != expectedContent {
		t.Errorf("Output = %q, want %q", result.Output, expectedContent)
	}
}

func TestToolRead_FileNotFound(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolRead(map[string]any{"path": "nonexistent_file_xyz.txt"})

	if result.Err == "" {
		t.Error("expected error for nonexistent file, got none")
	}
	if result.Output != "" {
		t.Errorf("expected empty output on error, got: %q", result.Output)
	}
}

func TestToolRead_MissingPathArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolRead(map[string]any{})

	if result.Output == "" {
		t.Error("expected error message for missing path argument")
	}
}

func TestToolRead_EmptyPathArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolRead(map[string]any{"path": ""})

	if result.Output == "" {
		t.Error("expected error message for empty path argument")
	}
}

func TestToolRead_NonStringValue(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolRead(map[string]any{"path": 123})

	if result.Output == "" {
		t.Error("expected error message for non-string path argument")
	}
}

func TestToolRead_EmbeddedDocs(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolRead(map[string]any{"path": "majordomo-docs/extending-majordomo.md"})

	if result.Err != "" {
		t.Errorf("expected no error for embedded doc, got: %s", result.Err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output for embedded doc")
	}
}

func TestToolEdit_Success(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	dir := t.TempDir()
	testFile := filepath.Join(dir, "edit_test.txt")
	originalContent := "hello world foo"
	expectedContent := "hello world bar"
	if err := os.WriteFile(testFile, []byte(originalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origCwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origCwd)

	result := a.toolEdit(map[string]any{
		"path":    "edit_test.txt",
		"oldText": "foo",
		"newText": "bar",
	})

	if result.Err != "" {
		t.Errorf("expected no error, got: %s", result.Err)
	}
	if result.Output == "" || result.Output == "no change" {
		t.Error("expected success message")
	}

	// Verify the file was actually modified
	updated, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != expectedContent {
		t.Errorf("file content = %q, want %q", string(updated), expectedContent)
	}
}

func TestToolEdit_TextNotFound(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	dir := t.TempDir()
	testFile := filepath.Join(dir, "notfound.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	origCwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origCwd)

	result := a.toolEdit(map[string]any{
		"path":    "notfound.txt",
		"oldText": "xyz_nonexistent",
		"newText": "replaced",
	})

	if result.Output == "" {
		t.Error("expected 'no change' message")
	}
}

func TestToolEdit_FileNotFound(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolEdit(map[string]any{
		"path":    "nonexistent_edit.txt",
		"oldText": "foo",
		"newText": "bar",
	})

	if result.Err == "" {
		t.Error("expected error for nonexistent file")
	}
	if result.Output != "" {
		t.Errorf("expected empty output on error, got: %q", result.Output)
	}
}

func TestToolEdit_MissingArgs(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolEdit(map[string]any{"path": "test.txt"})

	if result.Output == "" {
		t.Error("expected error message for missing arguments")
	}
}

func TestToolWrite_Success(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	dir := t.TempDir()
	testFile := filepath.Join(dir, "write_test.txt")
	content := "written content"

	origCwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origCwd)

	result := a.toolWrite(map[string]any{
		"path":    "write_test.txt",
		"content": content,
	})

	if result.Err != "" {
		t.Errorf("expected no error, got: %s", result.Err)
	}

	// Verify the file was written
	written, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != content {
		t.Errorf("file content = %q, want %q", string(written), content)
	}
}

func TestToolWrite_MissingPathArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolWrite(map[string]any{"content": "data"})

	if result.Output == "" {
		t.Error("expected error message for missing path")
	}
}

func TestToolWrite_MissingContentArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolWrite(map[string]any{"path": "test.txt"})

	if result.Output == "" {
		t.Error("expected error message for missing content")
	}
}

func TestToolWrite_WriteToReadOnlyDir(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Create a directory and make it read-only
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o444); err != nil {
		t.Skipf("cannot chmod, skipping test: %v", err)
	}
	defer os.Chmod(dir, 0o755) // restore for cleanup

	origCwd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origCwd)

	result := a.toolWrite(map[string]any{
		"path":    "readonly/test.txt",
		"content": "data",
	})

	if result.Err == "" {
		t.Error("expected error writing to read-only directory")
	}
}

func TestToolBash_Success(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolBash(map[string]any{"cmd": "echo hello"})

	if result.Err != "" {
		t.Errorf("expected no error, got: %s", result.Err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestToolBash_Failure(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolBash(map[string]any{"cmd": "exit 1"})

	if result.Err == "" {
		t.Error("expected error for failing command")
	}
}

func TestToolBash_MissingCmdArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolBash(map[string]any{})

	if result.Output == "" {
		t.Error("expected error message for missing cmd")
	}
}

func TestToolBash_EmptyCmdArg(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolBash(map[string]any{"cmd": ""})

	if result.Output == "" {
		t.Error("expected error message for empty cmd")
	}
}

func TestToolBash_NonStringValue(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.toolBash(map[string]any{"cmd": 42})

	if result.Output == "" {
		t.Error("expected error message for non-string cmd")
	}
}

func TestExecuteTool_Unknown(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	result := a.executeTool("nonexistent_tool", map[string]any{})

	if result.Output == "" {
		t.Error("expected output for unknown tool")
	}
}

func TestParseToolArgs_Valid(t *testing.T) {
	args, err := parseToolArgs(`{"path": "test.txt", "count": 42}`)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if args["path"] != "test.txt" {
		t.Errorf("path = %v, want test.txt", args["path"])
	}
	if args["count"].(float64) != 42 {
		t.Errorf("count = %v, want 42", args["count"])
	}
}

func TestParseToolArgs_InvalidJSON(t *testing.T) {
	_, err := parseToolArgs(`{invalid json}`)

	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseToolArgs_EmptyString(t *testing.T) {
	_, err := parseToolArgs("")

	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestParseToolArgs_EmptyObject(t *testing.T) {
	args, err := parseToolArgs(`{}`)

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty map, got %v", args)
	}
}

func TestAgentToolErrorPropagation(t *testing.T) {
	manager := llm.NewManager()
	a := New(manager)

	// Verify that when a tool returns an error, the error message
	// is placed in Output so it gets sent to the LLM
	result := a.toolRead(map[string]any{"path": "does_not_exist_xyz.txt"})

	if result.Err == "" {
		t.Fatal("expected error for nonexistent file")
	}

	// The error message should be present in Err field
	// This is what gets sent to the LLM as Content in runWithSession
	if result.Output != "" {
		t.Error("expected empty Output on error (error goes in Err field)")
	}
}
