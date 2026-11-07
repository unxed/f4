package panel

import "testing"

func TestDriveMenuSizeDecimalPrecisionAndUnits(t *testing.T) {
	tests := []struct {
		name    string
		bytes   uint64
		decimal bool
		want    string
	}{
		{"bytes", 512, true, "512 B"},
		{"one digit", 1536, true, "1.50 KB"},
		{"two digits", 10 * 1024, true, "10.0 KB"},
		{"three digits", 100 * 1024, true, "100 KB"},
		{"round up", 1024 + 512, true, "1.50 KB"},
		{"terabytes", 2 * 1024 * 1024 * 1024 * 1024, true, "2.00 TB"},
		{"integer units", 10 * 1024 * 1024, false, "10 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DriveMenuSize(tt.bytes, tt.decimal); got != tt.want {
				t.Errorf("DriveMenuSize(%d, %t) = %q, want %q", tt.bytes, tt.decimal, got, tt.want)
			}
		})
	}
}
