package progress

import "testing"

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1572864, "1.5MB"},
		{1073741824, "1.0GB"},
	}
	for _, tt := range tests {
		result := formatBytes(tt.input)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNewBar(t *testing.T) {
	bar := New("Upload", 1000)
	if bar.label != "Upload" || bar.total != 1000 {
		t.Fatalf("unexpected bar state: %+v", bar)
	}
}

func TestBarUpdateZeroTotal(t *testing.T) {
	bar := New("Test", 0)
	bar.Update(50) // should not panic
}
