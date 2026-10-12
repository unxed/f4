package vtvibe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The register of the user's orders (unxed/f4#1842, docs/VTVIBE.md § 19a,
// stage H4, first step). The main dialog is meant to be the user's
// secretary: no order may get lost and none may be left undone. Every
// message the user sends is entered here; the open ones are put in front of
// the model with every request, and the user closes or reopens them. The
// model closing them itself comes with the manager's tools.

// Order is one thing the user asked for.
type Order struct {
	ID     int       `json:"id"`
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
	Done   bool      `json:"done,omitempty"`
	DoneAt time.Time `json:"done_at,omitzero"`
}

// Orders returns a copy of the register, oldest first.
func (s *Session) Orders() []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Order(nil), s.orders...)
}

// SetOrderDone closes (done) or reopens (!done) the order with id.
func (s *Session) SetOrderDone(id int, done bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.orders {
		if s.orders[i].ID != id {
			continue
		}
		s.orders[i].Done = done
		s.orders[i].DoneAt = time.Time{}
		if done {
			s.orders[i].DoneAt = time.Now()
		}
		s.saveLocked()
		return nil
	}
	return fmt.Errorf("vtvibe: there is no order %d", id)
}

// addOrderLocked enters a message of the user. Caller holds s.mu.
func (s *Session) addOrderLocked(text string) {
	next := 1
	if n := len(s.orders); n > 0 {
		next = s.orders[n-1].ID + 1
	}
	s.orders = append(s.orders, Order{ID: next, Text: text, Time: time.Now()})
}

// ordersPromptLocked is the part of the system prompt that keeps the model
// on the open orders, including asking, the message being sent now; empty
// when none is open. Caller holds s.mu.
func (s *Session) ordersPromptLocked(asking string) string {
	var open []string
	next := 1
	for _, o := range s.orders {
		next = o.ID + 1
		if !o.Done {
			open = append(open, fmt.Sprintf("#%d: %s", o.ID, orderSummary(o.Text)))
		}
	}
	if asking != "" {
		open = append(open, fmt.Sprintf("#%d: %s", next, orderSummary(asking)))
	}
	if len(open) == 0 {
		return ""
	}
	return "The user's orders in this dialog that are not done yet. None of them may be lost or left undone: " +
		"when you answer, say which of them your answer carries out and which are still waiting. " +
		"If your answer fully carries out some of them, end it with one last line of the form " +
		ordersDoneMarker + " #1, #3 — never list an order there that is not completely done.\n" +
		strings.Join(open, "\n")
}

// ordersDoneMarker starts the last line in which the model reports the
// orders its answer carried out (f4#1842, stage H4): plain text, so it works
// with every provider and with streamed answers alike.
const ordersDoneMarker = "ORDERS DONE:"

var ordersDoneLine = regexp.MustCompile(`(?i)^\s*ORDERS DONE:\s*((?:#?\d+\s*,?\s*)+)$`)

// closeOrdersFromReplyLocked closes the open orders named on the reply's
// last line and puts a visible check mark in its place; a reply without the
// line is returned as it is. Caller holds s.mu.
func (s *Session) closeOrdersFromReplyLocked(reply string) string {
	trimmed := strings.TrimRight(reply, " \t\r\n")
	cut := strings.LastIndexByte(trimmed, '\n')
	last := trimmed[cut+1:]
	m := ordersDoneLine.FindStringSubmatch(last)
	if m == nil {
		return reply
	}
	var closed []string
	for _, field := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' || r == '#' }) {
		id, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		for i := range s.orders {
			if s.orders[i].ID == id && !s.orders[i].Done {
				s.orders[i].Done = true
				s.orders[i].DoneAt = time.Now()
				closed = append(closed, "#"+field)
			}
		}
	}
	body := strings.TrimRight(trimmed[:cut+1], " \t\r\n")
	if len(closed) == 0 {
		return body
	}
	mark := "✓ " + strings.Join(closed, " ")
	if body == "" {
		return mark
	}
	return body + "\n\n" + mark
}

// orderSummary is the first line of an order, cut at 300 characters: the
// whole text stays in the dialog itself, which is never shortened.
func orderSummary(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(line); len(r) > 300 {
		line = string(r[:300]) + "…"
	}
	return line
}

// AddOrder enters an order given outside the chat (a task handed to a
// worker) and returns its number.
func (s *Session) AddOrder(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addOrderLocked(text)
	s.saveLocked()
	return s.orders[len(s.orders)-1].ID
}
