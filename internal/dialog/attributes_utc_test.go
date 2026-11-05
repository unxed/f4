package dialog

import (
	"testing"
	"time"
)

func TestParseAttributesTimeLocalAndUTC(t *testing.T) {
	text := "04.03.2021 05:06:07"
	for _, tc := range []struct {
		name string
		utc  bool
		want *time.Location
	}{
		{"local", false, time.Local},
		{"UTC", true, time.UTC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAttributesTime(text, tc.utc)
			if err != nil {
				t.Fatal(err)
			}
			if got.Location() != tc.want {
				t.Errorf("location = %v, want %v", got.Location(), tc.want)
			}
		})
	}
}
