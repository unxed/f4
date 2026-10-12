package vtvibe

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The dialog's working mode (unxed/f4#1842, docs/VTVIBE.md § 19a.4, stage H6).
// In the question-and-answer mode the model answers the message and waits for
// the user. In the non-stop mode it goes on by itself, round after round,
// until every order of the user is done, it says it needs the user, or the
// round limit is reached.

// Mode is the working mode chosen for one dialog.
type Mode string

const (
	// ModeDefault follows the setting for new dialogs.
	ModeDefault Mode = ""
	// ModeQA answers and waits.
	ModeQA Mode = "qa"
	// ModeNonstop goes on until the orders are done.
	ModeNonstop Mode = "nonstop"
)

// ParseMode reads a mode as the user types it after ai:mode.
func ParseMode(text string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "nonstop", "non-stop", "auto":
		return ModeNonstop, nil
	case "qa", "q&a", "ask":
		return ModeQA, nil
	case "default":
		return ModeDefault, nil
	}
	return ModeDefault, fmt.Errorf("vtvibe: unknown mode %q", text)
}

// Mode returns the dialog's own mode; ModeDefault when none was chosen.
func (s *Session) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// SetMode chooses the dialog's mode; it is kept with the dialog.
func (s *Session) SetMode(m Mode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	s.saveLocked()
}

// MaxNonstopRounds bounds the rounds the model goes on by itself after one
// message of the user.
const MaxNonstopRounds = 10

// waitingMarker is the line with which the model in the non-stop mode says it
// cannot go on without the user.
const waitingMarker = "WAITING FOR USER"

var waitingLine = regexp.MustCompile(`(?im)^\s*WAITING FOR USER\b`)

func modePrompt(nonstop bool) string {
	if !nonstop {
		return "This dialog is in the question-and-answer mode: answer the message and stop. " +
			"When something is unclear, ask the user rather than guess."
	}
	return "This dialog is in the non-stop mode: do not stop to ask the user what you can decide yourself — " +
		"choose the reasonable option, say what you chose, and go on. While any order above is open, f4 asks you " +
		"to go on after your answer. If you cannot go on without the user (a decision only they can make, " +
		"something you cannot get yourself), say what you need and end your answer with a line " + waitingMarker + " (before the " + ordersDoneMarker + " line, if there is one)."
}

// WorkEnd says why Work returned.
type WorkEnd int

const (
	// WorkAnswered: the question-and-answer mode answered the message.
	WorkAnswered WorkEnd = iota
	// WorkOrdersDone: no order is open any more.
	WorkOrdersDone
	// WorkWaiting: the model said it needs the user.
	WorkWaiting
	// WorkRoundLimit: MaxNonstopRounds rounds went by with orders still open.
	WorkRoundLimit
	// WorkDelegated: the model handed tasks to workers (TakeDelegations);
	// the work goes on when their reports are in (Resume).
	WorkDelegated
)

// Work sends question like Ask. In the non-stop mode (the dialog's own, or
// nonstop when the dialog follows the default) it then asks the model to go
// on while orders are open. The rounds after the first are marked in the
// dialog as sent by f4, never as the user's words.
func (s *Session) Work(ctx context.Context, cfg Config, question string, nonstop bool) (WorkEnd, error) {
	switch s.Mode() {
	case ModeNonstop:
		nonstop = true
	case ModeQA:
		nonstop = false
	}
	reply, err := s.ask(ctx, cfg, question, true, modePrompt(nonstop))
	return s.goOn(ctx, cfg, nonstop, reply, err)
}

// Resume asks the model to go on with the open orders, as the non-stop mode
// does after an answer: the host calls it when the workers the model handed
// tasks to have reported. It does nothing when no order is open.
func (s *Session) Resume(ctx context.Context, cfg Config) (WorkEnd, error) {
	return s.goOn(ctx, cfg, true, "", nil)
}

func (s *Session) goOn(ctx context.Context, cfg Config, nonstop bool, reply string, err error) (WorkEnd, error) {
	for round := 1; ; round++ {
		if err != nil {
			return WorkAnswered, err
		}
		if s.hasDelegations() {
			return WorkDelegated, nil
		}
		if !nonstop {
			return WorkAnswered, nil
		}
		open := s.openOrderIDs()
		switch {
		case len(open) == 0:
			return WorkOrdersDone, nil
		case waitingLine.MatchString(reply):
			return WorkWaiting, nil
		case round > MaxNonstopRounds:
			return WorkRoundLimit, nil
		}
		reply, err = s.ask(ctx, cfg, goOnMessage(open), false, modePrompt(true))
	}
}

// goOnMessage is what f4 sends in the non-stop mode to keep the model going.
func goOnMessage(open []int) string {
	ids := make([]string, len(open))
	for i, id := range open {
		ids[i] = fmt.Sprintf("#%d", id)
	}
	return "[f4, non-stop mode] Go on with the open orders: " + strings.Join(ids, ", ") + "."
}

func (s *Session) openOrderIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var open []int
	for _, o := range s.orders {
		if !o.Done {
			open = append(open, o.ID)
		}
	}
	return open
}
