package vfs

import "sync/atomic"

// interactivePrompts counts the worker goroutines that are currently
// inside an interactive prompt cycle (for example the archive password
// dialog together with the retry it triggers). Progress screens consult
// it before appearing on top of the UI.
var interactivePrompts atomic.Int32

// HoldInteractivePrompt marks the start of an interactive prompt cycle run
// from a background worker. It returns the function that ends the cycle.
//
// A prompt cycle is not just one dialog: after the user answers, the worker
// retries the operation and may show the dialog again. Between those two
// dialogs no modal frame is on screen, so a delayed progress screen that
// only looks at the top frame would appear in that gap and the next prompt
// would land on top of it (issue #816). Holding the whole cycle keeps the
// progress screen away until the prompt is really over.
func HoldInteractivePrompt() (release func()) {
	interactivePrompts.Add(1)
	var released atomic.Bool
	return func() {
		if released.CompareAndSwap(false, true) {
			interactivePrompts.Add(-1)
		}
	}
}

// InteractivePromptPending reports whether some worker is inside an
// interactive prompt cycle started with HoldInteractivePrompt.
func InteractivePromptPending() bool {
	return interactivePrompts.Load() > 0
}
