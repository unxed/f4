package vtvibe

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSession_Draft(t *testing.T) {
	s := NewSession()
	if s.Draft() != "" {
		t.Errorf("expected empty draft, got %q", s.Draft())
	}
	if err := s.tree.writeFile("/draft.md", []byte("hello model")); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	if s.Draft() != "hello model" {
		t.Errorf("expected draft to be read")
	}
	s.ClearDraft()
	if s.Draft() != "" {
		t.Errorf("expected empty draft after clear")
	}
}

func TestSession_Reset(t *testing.T) {
	s := NewSession()
	if err := s.tree.writeFile("/ctx/test.go", []byte("package main")); err != nil {
		t.Fatalf("write context: %v", err)
	}
	s.appendTurn(Turn{Role: "user", Text: "hello"})

	s.Reset(true)
	if len(s.Turns()) != 1 {
		t.Errorf("expected 1 turn (RCtrl+A hint), got %d", len(s.Turns()))
	}
	if _, ok := s.tree.readFile("/ctx/test.go"); !ok {
		t.Errorf("expected context to be kept")
	}

	s.Reset(false)
	if _, ok := s.tree.readFile("/ctx/test.go"); ok {
		t.Errorf("expected context to be cleared")
	}
}
func TestSession_HasNewContextFiles(t *testing.T) {
	s := NewSession()

	// Case 1: No turns, should be false
	if s.HasNewContextFiles() {
		t.Error("Expected HasNewContextFiles to be false on empty session")
	}

	// Add user files to /ctx
	_ = s.tree.writeFile("/ctx/run.sh", []byte("echo hello"))

	// Add a turn
	s.appendTurn(Turn{Role: "user", Text: "Hello", Time: time.Now()})

	// Case 2: Files exist but are older than the turn
	if s.HasNewContextFiles() {
		t.Error("Expected HasNewContextFiles to be false when files are older than the turn")
	}

	// Add a new file after the turn
	time.Sleep(10 * time.Millisecond) // Ensure time advances
	_ = s.tree.writeFile("/ctx/new.go", []byte("package main"))

	// Case 3: A new file is added after the turn
	if !s.HasNewContextFiles() {
		t.Error("Expected HasNewContextFiles to be true when a new file is added after the turn")
	}
}
func TestAIVFS_CreateRedirectsToContextWhenCwdIsChat(t *testing.T) {
	s := NewSession()
	v := NewVFS(s)

	wc, err := v.Create(context.Background(), "/chat/status.sh")
	if err != nil {
		t.Fatalf("expected Create on /chat/status.sh to succeed, got %v", err)
	}
	_, _ = wc.Write([]byte("echo ok"))
	if err := wc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	files := s.ContextFiles()
	if len(files) != 1 || files[0] != "status.sh" {
		t.Fatalf("expected ContextFiles() = ['status.sh'], got %v", files)
	}

	r, err := v.Open(context.Background(), "/chat/status.sh")
	if err != nil {
		t.Fatalf("expected Open /chat/status.sh to find /ctx/status.sh, got %v", err)
	}
	_ = r.Close()
}

func TestAIVFS_ConcurrentReset(t *testing.T) {
	s := NewSession()
	v := NewVFS(s)
	ctx := context.Background()

	const iterations = 100
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			s.Reset(true)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			if _, err := v.Stat(ctx, "/"); err != nil {
				errs <- fmt.Errorf("Stat root: %w", err)
				return
			}
			if err := v.ReadDir(ctx, "/", nil); err != nil {
				errs <- fmt.Errorf("ReadDir root: %w", err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// f4#1842: a file pasted into the AI panel lands in /ctx; a second one with
// the same name gets a number instead of replacing the first.
func TestAddContextFileKeepsBothNames(t *testing.T) {
	s := NewSession()
	first, err := s.AddContextFile("shot.png", []byte("one"))
	if err != nil || first != "shot.png" {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, err := s.AddContextFile("dir/shot.png", []byte("two"))
	if err != nil || second != "shot (2).png" {
		t.Fatalf("second = %q, %v; want \"shot (2).png\"", second, err)
	}
	files := strings.Join(s.ContextFiles(), ",")
	if !strings.Contains(files, "shot.png") || !strings.Contains(files, "shot (2).png") {
		t.Errorf("context files = %s", files)
	}
}
