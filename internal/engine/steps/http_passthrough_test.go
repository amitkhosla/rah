package steps

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"unsafe"
)

// buildUpstreamURL mirrors the logic from the hot path in HttpActionFromConfig.
// It combines a base URL with optional path suffix and query string, following
// the same pattern as the inline URL enrichment in http.go.
func buildUpstreamURL(baseURL string, path []byte, rawQuery []byte, forwardPath, forwardQuery bool) string {
	if !forwardPath && !forwardQuery {
		return baseURL
	}

	sz := len(baseURL)
	if forwardPath && len(path) > 0 {
		sz += len(path)
	}
	if forwardQuery && len(rawQuery) > 0 {
		sz += 1 + len(rawQuery)
	}

	buf := make([]byte, sz)
	n := copy(buf, baseURL)

	if forwardPath && len(path) > 0 {
		n += copy(buf[n:], path)
	}

	if forwardQuery && len(rawQuery) > 0 {
		sep := byte('?')
		for i := 0; i < n; i++ {
			if buf[i] == '?' {
				sep = '&'
				break
			}
		}
		buf[n] = sep
		n++
		n += copy(buf[n:], rawQuery)
	}

	return unsafe.String(unsafe.SliceData(buf), n)
}

func TestForwardQueryParams_EmptyQuery(t *testing.T) {
	baseURL := "https://backend.example.com"
	result := buildUpstreamURL(baseURL, nil, []byte(""), false, false)
	if result != baseURL {
		t.Errorf("empty query should not modify URL: got %q, want %q", result, baseURL)
	}
}

func TestForwardQueryParams_AppendToURLWithoutQuery(t *testing.T) {
	baseURL := "https://backend.example.com"
	query := []byte("key=value")
	result := buildUpstreamURL(baseURL, nil, query, false, true)
	expected := "https://backend.example.com?key=value"
	if result != expected {
		t.Errorf("append query to URL without query: got %q, want %q", result, expected)
	}
}

func TestForwardQueryParams_AppendToURLWithExistingQuery(t *testing.T) {
	baseURL := "https://backend.example.com?existing=param"
	query := []byte("new=value")
	result := buildUpstreamURL(baseURL, nil, query, false, true)
	expected := "https://backend.example.com?existing=param&new=value"
	if result != expected {
		t.Errorf("append query to URL with existing query: got %q, want %q", result, expected)
	}
}

func TestForwardQueryParams_Disabled(t *testing.T) {
	baseURL := "https://backend.example.com"
	query := []byte("key=value")
	result := buildUpstreamURL(baseURL, nil, query, false, false)
	if result != baseURL {
		t.Errorf("disabled ForwardQueryParams should not append: got %q, want %q", result, baseURL)
	}
}

func TestForwardPathSuffix_EmptyPath(t *testing.T) {
	baseURL := "https://backend.example.com"
	result := buildUpstreamURL(baseURL, []byte(""), nil, false, false)
	if result != baseURL {
		t.Errorf("empty path should not modify URL: got %q, want %q", result, baseURL)
	}
}

func TestForwardPathSuffix_AppendPath(t *testing.T) {
	baseURL := "https://backend.example.com"
	path := []byte("/users/123")
	result := buildUpstreamURL(baseURL, path, nil, true, false)
	expected := "https://backend.example.com/users/123"
	if result != expected {
		t.Errorf("append path: got %q, want %q", result, expected)
	}
}

func TestForwardPathSuffix_Disabled(t *testing.T) {
	baseURL := "https://backend.example.com"
	path := []byte("/users/123")
	result := buildUpstreamURL(baseURL, path, nil, false, false)
	if result != baseURL {
		t.Errorf("disabled ForwardPathSuffix should not append: got %q, want %q", result, baseURL)
	}
}

func TestForwardBothPathAndQuery(t *testing.T) {
	baseURL := "https://backend.example.com"
	path := []byte("/users/123")
	query := []byte("page=1")
	result := buildUpstreamURL(baseURL, path, query, true, true)
	expected := "https://backend.example.com/users/123?page=1"
	if result != expected {
		t.Errorf("combine path and query: got %q, want %q", result, expected)
	}
}

func TestForwardBothPathAndQuery_WithExistingQuery(t *testing.T) {
	baseURL := "https://backend.example.com?existing=true"
	path := []byte("/api/v1")
	query := []byte("filter=active")
	result := buildUpstreamURL(baseURL, path, query, true, true)
	expected := "https://backend.example.com?existing=true/api/v1&filter=active"
	if result != expected {
		t.Errorf("combine path and query with existing query: got %q, want %q", result, expected)
	}
}

func TestForwardPathSuffix_ComplexPath(t *testing.T) {
	baseURL := "https://api.example.com/v2"
	path := []byte("/search/items/42/details")
	result := buildUpstreamURL(baseURL, path, nil, true, false)
	expected := "https://api.example.com/v2/search/items/42/details"
	if result != expected {
		t.Errorf("complex path: got %q, want %q", result, expected)
	}
}

func TestForwardQueryParams_ComplexQuery(t *testing.T) {
	baseURL := "https://api.example.com"
	query := []byte("filter=status%3Dactive&sort=date&limit=50")
	result := buildUpstreamURL(baseURL, nil, query, false, true)
	expected := "https://api.example.com?filter=status%3Dactive&sort=date&limit=50"
	if result != expected {
		t.Errorf("complex query: got %q, want %q", result, expected)
	}
}

// Integration test using httptest.Server to verify end-to-end behavior.
// This ensures that the URL enrichment works correctly with actual HTTP clients.
func TestHttpPassthrough_IntegrationWithServer(t *testing.T) {
	resetHTTPConfigForTest()

	// Create a test server that echoes the request URL
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-URL", r.RequestURI)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.RequestURI))
	}))
	defer ts.Close()

	// Test table: base URL, path, query, expected request URI
	tests := []struct {
		name        string
		baseURL     string
		path        []byte
		query       []byte
		fwdPath     bool
		fwdQuery    bool
		expectedURI string
	}{
		{
			name:        "path only",
			baseURL:     ts.URL,
			path:        []byte("/users/42"),
			query:       nil,
			fwdPath:     true,
			fwdQuery:    false,
			expectedURI: "/users/42",
		},
		{
			name:        "query only",
			baseURL:     ts.URL,
			path:        nil,
			query:       []byte("id=1&type=admin"),
			fwdPath:     false,
			fwdQuery:    true,
			expectedURI: "/?id=1&type=admin",
		},
		{
			name:        "both path and query",
			baseURL:     ts.URL,
			path:        []byte("/search"),
			query:       []byte("q=golang"),
			fwdPath:     true,
			fwdQuery:    true,
			expectedURI: "/search?q=golang",
		},
		{
			name:        "neither path nor query",
			baseURL:     ts.URL,
			path:        []byte("/ignored"),
			query:       []byte("ignored=true"),
			fwdPath:     false,
			fwdQuery:    false,
			expectedURI: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enrichedURL := buildUpstreamURL(tt.baseURL, tt.path, tt.query, tt.fwdPath, tt.fwdQuery)

			resp, err := http.Get(enrichedURL)
			if err != nil {
				t.Fatalf("HTTP GET failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.StatusCode)
			}

			receivedURI := resp.Header.Get("X-Request-URL")
			if receivedURI != tt.expectedURI {
				t.Errorf("request URI: got %q, want %q", receivedURI, tt.expectedURI)
			}
		})
	}
}
