package vtvibe

import (
	"regexp"
	"strconv"
	"strings"
)

// The manager hands work to workers (unxed/f4#1842, docs/VTVIBE.md § 19a.2,
// stage H5, second step). The main dialog is the user's secretary and does
// not do long work itself: when an order needs it, the model puts a line
// "WORKER TASK #N: ..." in its answer, and f4, once the user confirms, gives
// each such subtask to a worker in a clean context. The worker's report comes
// back into the dialog, where the manager sees it and closes the order. Plain
// text, like the orders' marker, so it works with every provider and with
// streamed answers.

// Delegation is one subtask the manager hands to a worker; Order is the
// order it serves, 0 when the model named none or one that is not open.
type Delegation struct {
	Order int
	Task  string
}

const delegateMarker = "WORKER TASK"

var delegateLine = regexp.MustCompile(`(?m)^[ \t]*WORKER TASK(?:[ \t]+#(\d+))?[ \t]*:[ \t]*(\S.*?)[ \t]*$`)

// parseDelegationsLocked finds the worker tasks of reply. Caller holds s.mu, so
// only open orders are taken as the order a task serves.
func (s *Session) parseDelegationsLocked(reply string) []Delegation {
	var out []Delegation
	for _, m := range delegateLine.FindAllStringSubmatch(reply, -1) {
		d := Delegation{Task: m[2]}
		if id, err := strconv.Atoi(m[1]); err == nil {
			for _, o := range s.orders {
				if o.ID == id && !o.Done {
					d.Order = id
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// SetDelegation lets the model hand tasks to workers; the host turns it on
// when it can run them. It is not kept with the dialog.
func (s *Session) SetDelegation(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delegate = on
}

// TakeDelegations returns the worker tasks of the answers since the last
// call and forgets them.
func (s *Session) TakeDelegations() []Delegation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.delegations
	s.delegations = nil
	return out
}

func (s *Session) hasDelegations() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.delegations) > 0
}

// managerPrompt tells the main dialog how to hand work out.
func managerPrompt() string {
	return strings.Join([]string{
		"You are the user's manager and secretary: you keep their orders and see every one done, but you do not do long work yourself.",
		"When an order needs real work on the user's files or programs (running commands, changing files, studying a project), hand it out:",
		"put a line of the form " + delegateMarker + " #N: <task> where N is the order it serves, one line per task.",
		"Each task goes to a worker manager, which splits it into subtasks for workers in clean contexts in the folder of the user's active panel,",
		"with a shell and file tools; none of them sees this dialog, so the line must say everything needed.",
		"The user confirms before the work starts; the workers' and the manager's reports come back into this dialog.",
		"Close an order only when the reports show it done.",
	}, " ")
}
