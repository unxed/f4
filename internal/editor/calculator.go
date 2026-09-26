package editor

import (
	"errors"
	"fmt"
	"strconv"
	"unicode"
)

// ErrCalculatorNoSelection is returned by CalculateSelection when there is no
// linear selection to evaluate.
var ErrCalculatorNoSelection = errors.New("select an expression first")

// errCalculatorDivisionByZero is a distinct sentinel so callers (and tests)
// can recognise this specific failure without parsing the message text.
var errCalculatorDivisionByZero = errors.New("division by zero")

// calcParser is a small recursive-descent parser/evaluator for basic
// arithmetic: + - * / ( ), decimal numbers, and unary +/-. It deliberately
// does not support variables, functions, or anything beyond that grammar —
// f4#1463 asks only for evaluating a selected arithmetic expression, not a
// scripting language.
type calcParser struct {
	expr string
	pos  int
}

// EvaluateArithmetic parses and evaluates a basic arithmetic expression with
// standard operator precedence (* and / bind tighter than + and -) and
// parentheses for grouping.
func EvaluateArithmetic(expr string) (float64, error) {
	p := &calcParser{expr: expr}
	p.skipSpace()
	if p.pos >= len(p.expr) {
		return 0, errors.New("expression is empty")
	}
	value, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos != len(p.expr) {
		return 0, fmt.Errorf("unexpected character %q at position %d", p.expr[p.pos], p.pos)
	}
	return value, nil
}

func (p *calcParser) skipSpace() {
	for p.pos < len(p.expr) && unicode.IsSpace(rune(p.expr[p.pos])) {
		p.pos++
	}
}

// parseExpr handles the lowest-precedence operators, + and -.
func (p *calcParser) parseExpr() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.expr) {
			return value, nil
		}
		switch p.expr[p.pos] {
		case '+':
			p.pos++
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value += rhs
		case '-':
			p.pos++
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value -= rhs
		default:
			return value, nil
		}
	}
}

// parseTerm handles * and /, which bind tighter than + and -.
func (p *calcParser) parseTerm() (float64, error) {
	value, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.expr) {
			return value, nil
		}
		switch p.expr[p.pos] {
		case '*':
			p.pos++
			rhs, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			value *= rhs
		case '/':
			p.pos++
			rhs, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			if rhs == 0 {
				return 0, errCalculatorDivisionByZero
			}
			value /= rhs
		default:
			return value, nil
		}
	}
}

// parseUnary handles a leading run of unary + or - signs, e.g. "-3" or
// "-(1+2)".
func (p *calcParser) parseUnary() (float64, error) {
	p.skipSpace()
	if p.pos < len(p.expr) {
		switch p.expr[p.pos] {
		case '-':
			p.pos++
			value, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			return -value, nil
		case '+':
			p.pos++
			return p.parseUnary()
		}
	}
	return p.parsePrimary()
}

// parsePrimary handles a number or a parenthesised sub-expression.
func (p *calcParser) parsePrimary() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.expr) {
		return 0, errors.New("unexpected end of expression")
	}
	if p.expr[p.pos] == '(' {
		p.pos++
		value, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.pos >= len(p.expr) || p.expr[p.pos] != ')' {
			return 0, errors.New("missing closing parenthesis")
		}
		p.pos++
		return value, nil
	}

	start := p.pos
	for p.pos < len(p.expr) && (p.expr[p.pos] == '.' || (p.expr[p.pos] >= '0' && p.expr[p.pos] <= '9')) {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("expected a number at position %d", p.pos)
	}
	value, err := strconv.ParseFloat(p.expr[start:p.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", p.expr[start:p.pos])
	}
	return value, nil
}

// FormatCalculatorResult renders a computed value as the calculator inserts
// it: the shortest decimal that round-trips, so whole-number results read as
// "8" rather than "8.000000".
func FormatCalculatorResult(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// CalculateSelection evaluates the editor's current linear selection as a
// basic arithmetic expression and replaces it with the computed result, as a
// single undoable edit — mirroring TransformBase64Selection's shape. A
// rectangular selection is rejected for the same reason base64 rejects one:
// several visual columns are not one expression.
func (ev *EditorView) CalculateSelection() (string, error) {
	if ev.RectSelActive {
		return "", errors.New("calculator requires a linear selection")
	}
	min, max := ev.GetSelectionRange()
	if max <= min {
		return "", ErrCalculatorNoSelection
	}
	data, err := ev.Pt.GetRange(min, max-min)
	if err != nil {
		return "", fmt.Errorf("read selection: %w", err)
	}

	result, err := EvaluateArithmetic(string(data))
	if err != nil {
		return "", err
	}

	formatted := FormatCalculatorResult(result)
	ev.replaceRange(min, max, []byte(formatted))
	return formatted, nil
}
