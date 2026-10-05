package apikey

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateApp_NoAppID_Returns400 tests that POST /apps without app_id returns 400.
// Expected: HTTP 400 with error message indicating app_id is required.
func TestCreateApp_NoAppID_Returns400(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{"name":"Test App"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "app_id is required") {
		t.Errorf("expected error message 'app_id is required', got %q", rr.Body.String())
	}
}

// TestCreateApp_WithAppID_Returns200 tests that POST /apps with app_id returns 200.
// Expected: HTTP 200 with the created app in the response body.
func TestCreateApp_WithAppID_Returns200(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{"app_id":42,"name":"Test App"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response App
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Errorf("failed to decode response: %v", err)
	}
	if response.AppID != 42 {
		t.Errorf("expected AppID=42, got %d", response.AppID)
	}
	if response.Name != "Test App" {
		t.Errorf("expected Name='Test App', got %q", response.Name)
	}
}

// TestCreateApp_SameID_SameName_Idempotent tests that creating the same app twice is idempotent.
// Expected: Both requests return 200, and the app is returned as-is on the second request.
func TestCreateApp_SameID_SameName_Idempotent(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{"app_id":99,"name":"Idempotent App","description":"Test"}`

	// First request
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Errorf("first request: expected status 200, got %d", rr1.Code)
	}

	var response1 App
	if err := json.NewDecoder(rr1.Body).Decode(&response1); err != nil {
		t.Errorf("first request: failed to decode response: %v", err)
	}
	if response1.AppID != 99 {
		t.Errorf("first request: expected AppID=99, got %d", response1.AppID)
	}

	// Second request with identical body (should be idempotent)
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("second request: expected status 200, got %d", rr2.Code)
	}

	var response2 App
	if err := json.NewDecoder(rr2.Body).Decode(&response2); err != nil {
		t.Errorf("second request: failed to decode response: %v", err)
	}
	if response2.AppID != 99 {
		t.Errorf("second request: expected AppID=99, got %d", response2.AppID)
	}
	if response2.Name != response1.Name {
		t.Errorf("second request: expected Name to match first response")
	}
}

// TestCreateApp_NoName_Returns400 tests that POST /apps without name returns 400.
// Expected: HTTP 400 with error message indicating name is required.
func TestCreateApp_NoName_Returns400(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{"app_id":10}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "name must not be empty") {
		t.Errorf("expected error message 'name must not be empty', got %q", rr.Body.String())
	}
}

// TestCreateApp_InvalidJSON_Returns400 tests that POST /apps with invalid JSON returns 400.
// Expected: HTTP 400 with error message indicating invalid JSON.
func TestCreateApp_InvalidJSON_Returns400(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{invalid json}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON") {
		t.Errorf("expected error message 'invalid JSON', got %q", rr.Body.String())
	}
}

// TestCreateApp_ConflictingAppID_Returns409 tests that creating an app with the same ID but different name returns 409.
// Expected: HTTP 409 with error message indicating app_id is already in use.
func TestCreateApp_ConflictingAppID_Returns409(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	// Create first app
	body1 := `{"app_id":77,"name":"First App"}`
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body1))
	mux.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first request failed: %d", rr1.Code)
	}

	// Try to create second app with same ID but different name
	body2 := `{"app_id":77,"name":"Different Name"}`
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body2))
	mux.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "app_id already in use") {
		t.Errorf("expected error message 'app_id already in use', got %q", rr2.Body.String())
	}
}

// TestCreateApp_WithDescription_Persists tests that description and labels are persisted.
// Expected: HTTP 200 with description and labels in the response.
func TestCreateApp_WithDescription_Persists(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	body := `{"app_id":55,"name":"App with Details","description":"A detailed app","labels":{"env":"test","team":"backend"}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response App
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Errorf("failed to decode response: %v", err)
	}
	if response.Description != "A detailed app" {
		t.Errorf("expected Description='A detailed app', got %q", response.Description)
	}
	if response.Labels["env"] != "test" {
		t.Errorf("expected label env=test, got %q", response.Labels["env"])
	}
	if response.Labels["team"] != "backend" {
		t.Errorf("expected label team=backend, got %q", response.Labels["team"])
	}
}

// TestCreateApp_MultipleApps tests that multiple apps with different IDs can be created.
// Expected: All requests return 200 and apps have correct IDs.
func TestCreateApp_MultipleApps(t *testing.T) {
	server := NewServer(nil)
	mux := http.NewServeMux()
	server.RegisterHandlers(mux)

	testCases := []struct {
		appID uint32
		name  string
	}{
		{1, "App One"},
		{2, "App Two"},
		{3, "App Three"},
	}

	for _, tc := range testCases {
		body := fmt.Sprintf(`{"app_id":%d,"name":"%s"}`, tc.appID, tc.name)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/apps", strings.NewReader(body))
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("app_id %d: expected status 200, got %d", tc.appID, rr.Code)
		}

		var response App
		if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
			t.Errorf("app_id %d: failed to decode response: %v", tc.appID, err)
		}
		if response.AppID != tc.appID {
			t.Errorf("app_id %d: expected AppID=%d, got %d", tc.appID, tc.appID, response.AppID)
		}
	}
}
