package ingest

import (
	"bytes"
	"fmt"
	"text/template"
)

// Formatter serialises a batch of Events into bytes for a sink.
// Implementations are stateless and safe for concurrent use.
type Formatter interface {
	// Format serialises events into the target format.
	// The returned slice is owned by the caller.
	Format(events []Event) ([]byte, error)
	// ContentType returns the MIME type for HTTP sinks.
	ContentType() string
}

// ── NDJSON formatter ─────────────────────────────────────────────────────────

// NDJSONFormatter writes one JSON object per line (newline-delimited JSON).
// This is the default format.
type NDJSONFormatter struct{}

func (NDJSONFormatter) ContentType() string { return "application/x-ndjson" }

func (NDJSONFormatter) Format(events []Event) ([]byte, error) {
	var buf bytes.Buffer
	for i := range events {
		b, err := events[i].MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("ndjson marshal event %d: %w", i, err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// ── JSON array formatter ─────────────────────────────────────────────────────

// JSONArrayFormatter writes the batch as a single JSON array: [{...},{...}].
// Suitable for HTTP webhooks that expect an array body.
type JSONArrayFormatter struct{}

func (JSONArrayFormatter) ContentType() string { return "application/json" }

func (JSONArrayFormatter) Format(events []Event) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i := range events {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, err := events[i].MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("json_array marshal event %d: %w", i, err)
		}
		buf.Write(b)
	}
	buf.WriteByte(']')
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// ── Text formatter ────────────────────────────────────────────────────────────

// TextFormatter renders each event using a Go text/template.
// Template fields correspond to the exported Event accessors:
//
//	{{.TenantID}} {{.APIID}} {{.Kind}} {{.Model}} {{.SessionID}} {{.TxID}}
//	{{.TimestampNs}} {{.DurationNs}} {{.InputTokens}} {{.OutputTokens}}
//
// Example template:
//
//	{{.TimestampNs}} [{{.Kind}}] tenant={{.TenantID}} model={{.Model}}
type TextFormatter struct {
	tmpl *template.Template
}

// textEvent is the template data — exposes Event fields with exported names.
type textEvent struct {
	TenantID     uint16
	APIID        uint32
	Kind         string
	Model        string
	SessionID    string
	TxID         string
	TimestampNs  int64
	DurationNs   int64
	InputTokens  int32
	OutputTokens int32
}

func (f *TextFormatter) ContentType() string { return "text/plain; charset=utf-8" }

func (f *TextFormatter) Format(events []Event) ([]byte, error) {
	var buf bytes.Buffer
	for i := range events {
		e := &events[i]
		td := textEvent{
			TenantID:     e.TenantID,
			APIID:        e.APIID,
			Kind:         string(e.Kind),
			Model:        e.Model,
			SessionID:    e.SessionID,
			TxID:         e.TxID,
			TimestampNs:  e.TimestampNs,
			DurationNs:   e.DurationNs,
			InputTokens:  e.InputTokens,
			OutputTokens: e.OutputTokens,
		}
		if err := f.tmpl.Execute(&buf, td); err != nil {
			return nil, fmt.Errorf("text format event %d: %w", i, err)
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// ── Registry ─────────────────────────────────────────────────────────────────

// NewFormatter creates a Formatter from a format name and optional template string.
// Valid formats: "ndjson" (default), "json_array", "text".
// When format is "text", templateStr must be a valid Go text/template.
func NewFormatter(format, templateStr string) (Formatter, error) {
	switch format {
	case "", "ndjson":
		return NDJSONFormatter{}, nil
	case "json_array":
		return JSONArrayFormatter{}, nil
	case "text":
		if templateStr == "" {
			return nil, fmt.Errorf("format=text requires a non-empty template string")
		}
		tmpl, err := template.New("event").Parse(templateStr)
		if err != nil {
			return nil, fmt.Errorf("text template parse error: %w", err)
		}
		return &TextFormatter{tmpl: tmpl}, nil
	default:
		return nil, fmt.Errorf("unknown format %q (valid: ndjson, json_array, text)", format)
	}
}
