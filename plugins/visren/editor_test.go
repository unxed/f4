package visren

import (
	"strings"
	"testing"
)

func TestEditorRoundTrip(t *testing.T) {
	rows := []Preview{{Item: testItem("one.txt"), Destination: "1.txt"}, {Item: testItem("two.txt"), Destination: "2.txt"}}
	for _, two := range []bool{false, true} {
		data := renderEditorList(rows, two)
		if strings.Contains(string(data), `"`) {
			t.Fatalf("two=%v rendered quoted names:\n%s", two, data)
		}
		got, line, err := parseEditorList(data, rows, two)
		if err != nil || line != -1 || strings.Join(got, ",") != "1.txt,2.txt" {
			t.Fatalf("two=%v got=%v line=%d err=%v\n%s", two, got, line, err, data)
		}
	}
}

func TestEditorRoundTripKeepsSpacesWithoutQuotes(t *testing.T) {
	rows := []Preview{
		{Item: testItem("old name.txt"), Destination: "new name.txt"},
		{Item: testItem("folder name"), Destination: "renamed folder"},
	}
	for _, two := range []bool{false, true} {
		data := renderEditorList(rows, two)
		if strings.Contains(string(data), `"`) {
			t.Fatalf("two=%v rendered quoted names:\n%s", two, data)
		}
		got, line, err := parseEditorList(data, rows, two)
		if err != nil || line != -1 || strings.Join(got, ",") != "new name.txt,renamed folder" {
			t.Fatalf("two=%v got=%v line=%d err=%v\n%s", two, got, line, err, data)
		}
	}
}

func TestEditorRejectsChangedSourceAndRowCount(t *testing.T) {
	rows := []Preview{{Item: testItem("one.txt"), Destination: "1.txt"}}
	if _, line, err := parseEditorList([]byte("\"other.txt\" \"1.txt\"\n"), rows, true); err == nil || line != 0 {
		t.Fatalf("changed source accepted: line=%d err=%v", line, err)
	}
	if _, _, err := parseEditorList([]byte("\"1.txt\"\n\"2.txt\"\n"), rows, false); err == nil {
		t.Fatal("extra row accepted")
	}
}
