package vfs

import "testing"

func TestHoldInteractivePrompt(t *testing.T) {
	if InteractivePromptPending() {
		t.Fatal("prompt pending before any hold")
	}
	first := HoldInteractivePrompt()
	second := HoldInteractivePrompt()
	if !InteractivePromptPending() {
		t.Fatal("prompt not pending while held")
	}
	first()
	first() // a second release of the same hold is a no-op
	if !InteractivePromptPending() {
		t.Fatal("releasing one hold twice dropped the other hold")
	}
	second()
	if InteractivePromptPending() {
		t.Fatal("prompt still pending after all holds released")
	}
}
