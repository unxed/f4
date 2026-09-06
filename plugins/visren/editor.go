package visren

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

func renderEditorList(rows []Preview, twoColumns bool) []byte {
	var out strings.Builder
	maxSource := 0
	for _, row := range rows {
		if n := len(row.Item.Source); n > maxSource {
			maxSource = n
		}
	}
	for _, row := range rows {
		if twoColumns {
			fmt.Fprintf(&out, "%-*s %s\n", maxSource, row.Item.Source, row.Destination)
		} else {
			fmt.Fprintf(&out, "%s\n", row.Destination)
		}
	}
	return []byte(out.String())
}

func parseEditorList(data []byte, rows []Preview, twoColumns bool) ([]string, int, error) {
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(string(data), "\r\n", "\n")))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) != len(rows) {
		return nil, minInt(len(lines), len(rows)), fmt.Errorf("expected %d rows, got %d", len(rows), len(lines))
	}

	destinations := make([]string, len(rows))
	remainingRows := rows
	for idx, line := range lines {
		if len(remainingRows) == 0 {
			return nil, idx, fmt.Errorf("line %d has no corresponding preview row", idx+1)
		}
		row := remainingRows[0]
		remainingRows = remainingRows[1:]
		values, err := editorFields(line, row, twoColumns)
		expected := 1
		if twoColumns {
			expected = 2
		}
		if err != nil || len(values) != expected {
			if err == nil {
				err = fmt.Errorf("expected %d quoted column(s)", expected)
			}
			return nil, idx, fmt.Errorf("line %d: %w", idx+1, err)
		}
		if twoColumns && values[0] != row.Item.Source {
			return nil, idx, fmt.Errorf("line %d: source column was changed", idx+1)
		}
		destination := values[len(values)-1]
		if destination != row.Item.Source {
			if err := ValidateFilename(destination); err != nil {
				return nil, idx, fmt.Errorf("line %d: %w", idx+1, err)
			}
		}
		destinations[idx] = destination
	}
	return destinations, -1, nil
}

// editorFields accepts the current unquoted fixed-column format and the old
// quoted format. In source+target mode the known source name disambiguates
// spaces in both columns without making the user edit shell-style quotes.
func editorFields(line string, row Preview, twoColumns bool) ([]string, error) {
	values, quotedErr := quotedFields(line)
	if quotedErr == nil {
		return values, nil
	}
	if !twoColumns {
		if line == "" {
			return nil, quotedErr
		}
		return []string{line}, nil
	}
	if !strings.HasPrefix(line, row.Item.Source) {
		return nil, fmt.Errorf("source column was changed")
	}
	remainder := line[len(row.Item.Source):]
	if len(remainder) == 0 || (remainder[0] != ' ' && remainder[0] != '\t') {
		return nil, fmt.Errorf("source column was changed")
	}
	destination := strings.TrimLeft(remainder, " \t")
	if destination == "" {
		return nil, fmt.Errorf("empty destination")
	}
	return []string{row.Item.Source, destination}, nil
}

func quotedFields(line string) ([]string, error) {
	var values []string
	for pos := 0; pos < len(line); {
		for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
			pos++
		}
		if pos == len(line) {
			break
		}
		if line[pos] != '"' {
			return nil, fmt.Errorf("unexpected text outside quotes")
		}
		end := pos + 1
		escaped := false
		for end < len(line) {
			if escaped {
				escaped = false
			} else if line[end] == '\\' {
				escaped = true
			} else if line[end] == '"' {
				break
			}
			end++
		}
		if end >= len(line) {
			return nil, fmt.Errorf("unterminated quoted name")
		}
		value, err := strconv.Unquote(line[pos : end+1])
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		pos = end + 1
	}
	return values, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
