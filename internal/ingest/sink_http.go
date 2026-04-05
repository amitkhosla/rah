package ingest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPSink POSTs pre-formatted bytes to a configured URL.
type HTTPSink struct {
	name        string
	url         string
	headers     map[string]string
	contentType string
	client      *http.Client
}

// NewHTTPSink creates an HTTP sink.
// contentType is the MIME type set on every POST (e.g. "application/x-ndjson").
// timeoutMs 0 defaults to 5000.
func NewHTTPSink(name, url string, headers map[string]string, timeoutMs int, contentType string) *HTTPSink {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if contentType == "" {
		contentType = "application/json"
	}
	return &HTTPSink{
		name:        name,
		url:         url,
		headers:     headers,
		contentType: contentType,
		client:      &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond},
	}
}

func (s *HTTPSink) Name() string { return s.name }

func (s *HTTPSink) Write(formatted []byte) error {
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(formatted))
	if err != nil {
		return fmt.Errorf("http sink build request: %w", err)
	}
	req.Header.Set("Content-Type", s.contentType)
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("http sink do: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http sink upstream status %d", resp.StatusCode)
	}
	return nil
}

func (s *HTTPSink) Close() error { return nil }
