package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func TestMainBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "./bin/majordomo", "./cmd/majordomo")
	cmd.Dir = projectRoot()

	stderr, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build: %v\n%s", err, stderr)
	}
}
