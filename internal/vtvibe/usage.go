package vtvibe

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// What a dialog has spent (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage H9,
// item 1). The other harnesses show the tokens and the cost of a session;
// vtvibe counted only the last request. Now every request of the dialog —
// its own, its workers', its bot's — is added up per model, kept with the
// dialog, and priced where the service publishes prices (OpenRouter does in
// its model list; the others only report tokens).

// Spent returns what the dialog has spent so far, by model.
func (s *Session) Spent() map[string]Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Usage, len(s.spent))
	for m, u := range s.spent {
		out[m] = u
	}
	return out
}

// AddSpent adds what a worker or the bot spent on behalf of the dialog.
func (s *Session) AddSpent(model string, u Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addSpentLocked(model, u)
	s.saveLocked()
}

func (s *Session) addSpentLocked(model string, u Usage) {
	if u.In == 0 && u.Out == 0 {
		return
	}
	if s.spent == nil {
		s.spent = map[string]Usage{}
	}
	t := s.spent[model]
	t.In += u.In
	t.Out += u.Out
	s.spent[model] = t
}

// TotalSpent adds up the tokens of all models.
func TotalSpent(spent map[string]Usage) Usage {
	var t Usage
	for _, u := range spent {
		t.In += u.In
		t.Out += u.Out
	}
	return t
}

// ModelCost is what one model cost the dialog.
type ModelCost struct {
	Model string
	Usage Usage
	// Cost in the service's currency (US dollars for OpenRouter); valid
	// only when Priced.
	Cost   float64
	Priced bool
}

// Costs prices spent with the models' prices, most expensive first, then
// the unpriced ones by name.
func Costs(spent map[string]Usage, models []ModelInfo) []ModelCost {
	prices := make(map[string]ModelInfo, len(models))
	for _, m := range models {
		prices[m.ID] = m
	}
	out := make([]ModelCost, 0, len(spent))
	for model, u := range spent {
		c := ModelCost{Model: model, Usage: u}
		if p, ok := prices[model]; ok && p.Priced {
			c.Cost, c.Priced = float64(u.In)*p.PriceIn+float64(u.Out)*p.PriceOut, true
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priced != out[j].Priced {
			return out[i].Priced
		}
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// FormatTokens writes a token count short: 950, 12.3k, 4.1M.
func FormatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.Itoa(n)
}

// price reads a per-token price that may come as a string or a number.
func price(raw json.RawMessage) (float64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0, false
	}
	f, err := strconv.ParseFloat(text, 64)
	return f, err == nil && f >= 0
}
