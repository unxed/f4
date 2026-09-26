package editor

import (
	"errors"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
)

func TestEvaluateArithmetic(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{"addition", "2 + 2", 4},
		{"precedence", "2 + 2 * 3", 8},
		{"parentheses override precedence", "(2 + 2) * 3", 12},
		{"nested parentheses", "((1 + 2) * (3 + 4))", 21},
		{"division", "10 / 4", 2.5},
		{"decimals", "1.5 + 2.25", 3.75},
		{"leading unary minus", "-3 + 5", 2},
		{"unary minus before group", "-(2 + 3)", -5},
		{"double unary", "--5", 5},
		{"unary plus", "+5 - 2", 3},
		{"whitespace and newlines", "  1\n+  2 * 3  ", 7},
		{"single number", "42", 42},
		{"single decimal", ".5", 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateArithmetic(tt.expr)
			if err != nil {
				t.Fatalf("EvaluateArithmetic(%q) error = %v", tt.expr, err)
			}
			if got != tt.want {
				t.Fatalf("EvaluateArithmetic(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestEvaluateArithmeticErrors(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"trailing operator", "2 +"},
		{"unmatched opening paren", "(1 + 2"},
		{"unmatched closing paren", "1 + 2)"},
		{"unknown character", "2 + a"},
		{"two numbers with no operator", "2 2"},
		{"empty parentheses", "()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := EvaluateArithmetic(tt.expr); err == nil {
				t.Fatalf("EvaluateArithmetic(%q) unexpectedly succeeded", tt.expr)
			}
		})
	}
}

func TestEvaluateArithmeticDivisionByZero(t *testing.T) {
	_, err := EvaluateArithmetic("1 / 0")
	if !errors.Is(err, errCalculatorDivisionByZero) {
		t.Fatalf("EvaluateArithmetic(%q) error = %v, want division-by-zero", "1 / 0", err)
	}

	// Division by a zero-valued sub-expression must also be caught.
	_, err = EvaluateArithmetic("1 / (2 - 2)")
	if !errors.Is(err, errCalculatorDivisionByZero) {
		t.Fatalf("EvaluateArithmetic with zero sub-expression error = %v, want division-by-zero", err)
	}
}

func TestEditorCalculateSelectionReplacesWithResult(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("2 + 2 * 3")), nil, "test.txt")
	defer ev.Close()

	selectEditorBytes(ev, len("2 + 2 * 3"))
	result, err := ev.CalculateSelection()
	if err != nil {
		t.Fatalf("CalculateSelection: %v", err)
	}
	if result != "8" {
		t.Fatalf("CalculateSelection result = %q, want %q", result, "8")
	}
	if got := ev.GetText(); got != "8" {
		t.Fatalf("text after calculation = %q, want %q", got, "8")
	}
}

func TestEditorCalculateSelectionRequiresSelection(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("2 + 2")), nil, "test.txt")
	defer ev.Close()

	_, err := ev.CalculateSelection()
	if !errors.Is(err, ErrCalculatorNoSelection) {
		t.Fatalf("CalculateSelection with no selection error = %v, want ErrCalculatorNoSelection", err)
	}
}

func TestEditorCalculateSelectionRejectsRectangularSelection(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("2 + 2")), nil, "test.txt")
	defer ev.Close()
	ev.RectSelActive = true

	_, err := ev.CalculateSelection()
	if err == nil || errors.Is(err, ErrCalculatorNoSelection) {
		t.Fatalf("CalculateSelection with rectangular selection error = %v", err)
	}
}

func TestEditorCalculateSelectionReportsInvalidExpression(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("2 + ")), nil, "test.txt")
	defer ev.Close()

	before := ev.GetText()
	selectEditorBytes(ev, len("2 + "))
	if _, err := ev.CalculateSelection(); err == nil {
		t.Fatal("CalculateSelection accepted an invalid expression")
	}
	if got := ev.GetText(); got != before {
		t.Fatalf("text changed after invalid expression: %q", got)
	}
}
