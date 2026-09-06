package main

import "testing"

// A start with no terminal -- Dock, the detached GUI copy, the daemon -- names
// no startup directory, so the panels keep what the session restored. `go test`
// runs without a terminal on stdin, which is exactly that case.
func TestRememberStartupDirIgnoresStartWithoutTerminal(t *testing.T) {
	t.Setenv(startupDirEnv, "")
	rememberStartupDir()
	if got := startupDir(); got != "" {
		t.Fatalf("startupDir() = %q, want empty for a start without a terminal", got)
	}
}

// The parent's answer travels through the environment; the child inherits it.
func TestRememberStartupDirKeepsInheritedValue(t *testing.T) {
	t.Setenv(startupDirEnv, "/from/parent")
	rememberStartupDir()
	if got := startupDir(); got != "/from/parent" {
		t.Fatalf("startupDir() = %q, want the inherited value", got)
	}
}
