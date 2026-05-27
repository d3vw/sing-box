package option

import (
	"testing"
)

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"", 0, false},
		{"1000", 1000, false},
		{"1000b/s", 1000, false},
		{"1000B/s", 1000, false},
		{"8bps", 1, false},
		{"100Mbps", 12500000, false},
		{"100 Mbps", 12500000, false},
		{"10MB/s", 10485760, false},
		{"10 MB/s", 10485760, false},
		{"512KB/s", 524288, false},
		{"1.5Gbps", 187500000, false},
		{"100Kbps", 12500, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRateLimit(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRateLimit(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ParseRateLimit(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
