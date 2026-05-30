package gatewaylog

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestLevelGate verifies that Debug message is not emitted when level=INFO,
// but Info message is emitted.
func TestLevelGate(t *testing.T) {
	var buf bytes.Buffer
	oldOutput := log.Writer()
	defer log.SetOutput(oldOutput)
	log.SetOutput(&buf)

	logger := New(INFO)

	// Debug should not be emitted
	logger.Debug("debug message")
	if strings.Contains(buf.String(), "debug message") {
		t.Error("Debug message should not be emitted at INFO level")
	}

	buf.Reset()

	// Info should be emitted
	logger.Info("info message")
	if !strings.Contains(buf.String(), "info message") {
		t.Error("Info message should be emitted at INFO level")
	}
}

// TestParseLevel verifies that ParseLevel handles various inputs correctly.
func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected Level
	}{
		{"debug", DEBUG},
		{"DEBUG", DEBUG},
		{"info", INFO},
		{"INFO", INFO},
		{"warn", WARN},
		{"WARN", WARN},
		{"error", ERROR},
		{"ERROR", ERROR},
		{"unknown", INFO},
		{"", INFO},
	}

	for _, tt := range tests {
		got := ParseLevel(tt.input)
		if got != tt.expected {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// TestFormat verifies the logfmt output shape by capturing log output.
func TestFormat(t *testing.T) {
	var buf bytes.Buffer
	oldOutput := log.Writer()
	defer log.SetOutput(oldOutput)
	log.SetOutput(&buf)

	logger := New(DEBUG)

	// Test with msg containing spaces (should be quoted)
	logger.Info("config loaded", F("path", "/etc/rah/gateway.yaml"), Fint("stores", 3))
	output := buf.String()

	if !strings.Contains(output, `level=INFO`) {
		t.Error("Output should contain level=INFO")
	}
	if !strings.Contains(output, `msg="config loaded"`) {
		t.Error("Output should have quoted msg with spaces")
	}
	if !strings.Contains(output, `path=/etc/rah/gateway.yaml`) {
		t.Error("Output should contain unquoted field without spaces")
	}
	if !strings.Contains(output, `stores=3`) {
		t.Error("Output should contain unquoted numeric field")
	}

	buf.Reset()

	// Test with field value containing spaces (should be quoted)
	logger.Warn("rate limit fallback", F("reason", "redis unavailable"))
	output = buf.String()

	if !strings.Contains(output, `level=WARN`) {
		t.Error("Output should contain level=WARN")
	}
	if !strings.Contains(output, `reason="redis unavailable"`) {
		t.Error("Output should have quoted field with spaces")
	}
}
