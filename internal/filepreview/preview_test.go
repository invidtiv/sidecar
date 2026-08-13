package filepreview

import "testing"

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{"text", []byte("hello world"), false},
		{"binary with null", []byte("hello\x00world"), true},
		{"empty", []byte{}, false},
		{"binary at start", []byte{0, 1, 2, 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBinary(tt.data); got != tt.expected {
				t.Errorf("isBinary(%v) = %v, want %v", tt.data, got, tt.expected)
			}
		})
	}
}
