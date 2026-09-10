package internal

import "testing"

func TestQuickCISmoke(t *testing.T) {
	if 2+2 != 4 {
		t.Fatal("арифметика сломалась")
	}
}
