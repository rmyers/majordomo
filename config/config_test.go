package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := New(tmpDir)

	if cfg.Dir() != tmpDir {
		t.Errorf("expected dir %q, got %q", tmpDir, cfg.Dir())
	}

	expectedPath := filepath.Join(tmpDir, "config.json")
	if cfg.Path() != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, cfg.Path())
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Dir() == "" {
		t.Error("expected non-empty dir from Default()")
	}
}

func TestGetSetters(t *testing.T) {
	cfg := New(t.TempDir())

	cfg.SetModel("test-model")
	if cfg.GetModel() != "test-model" {
		t.Errorf("expected model 'test-model', got %q", cfg.GetModel())
	}

	cfg.SetURL("http://localhost:11434")
	if cfg.GetURL() != "http://localhost:11434" {
		t.Errorf("expected URL 'http://localhost:11434', got %q", cfg.GetURL())
	}

	cfg.SetAPIKey("secret-key")
	if cfg.GetAPIKey() != "secret-key" {
		t.Errorf("expected API key 'secret-key', got %q", cfg.GetAPIKey())
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := New(tmpDir)
	cfg.SetModel("gpt-4")
	cfg.SetURL("http://localhost:8080")
	cfg.SetAPIKey("api-key-123")

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Create a new Config pointing to the same directory and load
	cfg2 := New(tmpDir)
	if err := cfg2.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg2.GetModel() != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", cfg2.GetModel())
	}

	if cfg2.GetURL() != "http://localhost:8080" {
		t.Errorf("expected URL 'http://localhost:8080', got %q", cfg2.GetURL())
	}

	if cfg2.GetAPIKey() != "api-key-123" {
		t.Errorf("expected API key 'api-key-123', got %q", cfg2.GetAPIKey())
	}
}

func TestSaveToNonExistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "dir")

	cfg := New(nestedDir)
	cfg.SetModel("test")

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() should create parent dirs, error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(nestedDir, "config.json")); err != nil {
		t.Fatalf("config.json should exist after Save()")
	}
}

func TestLoadMissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := New(tmpDir)
	err := cfg.Load()

	if err == nil {
		t.Error("expected error when loading missing config file")
	}
}

func TestGetSessionsDir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := New(tmpDir)
	sessionsDir, err := cfg.GetSessionsDir()
	if err != nil {
		t.Fatalf("GetSessionsDir() error: %v", err)
	}

	expected := filepath.Join(tmpDir, "sessions")
	if sessionsDir != expected {
		t.Errorf("expected sessions dir %q, got %q", expected, sessionsDir)
	}
}
