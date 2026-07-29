package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseCreateAndRollbackDeploy(t *testing.T) {
	calls := 0
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer mgmt.Close()

	s, err := NewServer("", ServerConfig{Targets: []Target{{Name: "gw-dev", Level: "dev", URLs: []string{mgmt.URL}}}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	create := `{"release_id":"rel-2026.03.1","instruction_set_version":"instr-v7","api_versions":{"orders":"v2"},"levels":["dev"],"payload":{"sync_uuid":"abc","flows":[],"apis":[]}}`
	rr1 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr1, httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(create)))
	if rr1.Code != http.StatusOK {
		t.Fatalf("create deploy failed: %d %s", rr1.Code, rr1.Body.String())
	}

	rollback := `{"release_id":"rel-2026.03.1","levels":["dev"]}`
	rr2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(rollback)))
	if rr2.Code != http.StatusOK {
		t.Fatalf("rollback deploy failed: %d %s", rr2.Code, rr2.Body.String())
	}
	if calls != 2 {
		t.Fatalf("expected 2 deployments, got %d", calls)
	}
}

func TestOpenAPIImportJSONAndYAML(t *testing.T) {
	s, _ := NewServer("http://127.0.0.1:8081", ServerConfig{})
	jsonSpec := `{"openapi":"3.0.0","paths":{"/orders":{"get":{"operationId":"listOrders"}}}}`
	rrJSON := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrJSON, httptest.NewRequest(http.MethodPost, "/api/openapi/import", strings.NewReader(`{"spec":`+strconv.Quote(jsonSpec)+`}`)))
	if rrJSON.Code != http.StatusOK || !strings.Contains(rrJSON.Body.String(), "listOrders") {
		t.Fatalf("json import failed: %d %s", rrJSON.Code, rrJSON.Body.String())
	}
	yamlSpec := "openapi: 3.0.0\npaths:\n  /users:\n    post:\n      operationId: createUser\n"
	rrYAML := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrYAML, httptest.NewRequest(http.MethodPost, "/api/openapi/import", strings.NewReader(`{"spec":`+strconv.Quote(yamlSpec)+`}`)))
	if rrYAML.Code != http.StatusOK || !strings.Contains(rrYAML.Body.String(), "createUser") {
		t.Fatalf("yaml import failed: %d %s", rrYAML.Code, rrYAML.Body.String())
	}
}

func TestTargetsIncludesStoreAndReleases(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer mgmt.Close()
	s, _ := NewServer("", ServerConfig{Targets: []Target{{Name: "gw-dev", Level: "dev", URLs: []string{mgmt.URL}}}})
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(`{"release_id":"r1","levels":["dev"],"payload":{"sync_uuid":"abc"}}`)))

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/targets", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("targets status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "stores_supported") || !strings.Contains(body, "releases") || !strings.Contains(body, "r1") {
		t.Fatalf("missing expected fields: %s", body)
	}
}

func TestSchemaAndUIServed(t *testing.T) {
	s, _ := NewServer("http://127.0.0.1:8081", ServerConfig{})
	rrUI := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrUI, httptest.NewRequest(http.MethodGet, "/", nil))
	if rrUI.Code != http.StatusOK || !strings.Contains(rrUI.Body.String(), "RAH Studio") || !strings.Contains(rrUI.Body.String(), `id="root"`) {
		t.Fatalf("ui not served")
	}
	rrSchema := httptest.NewRecorder()
	s.Handler().ServeHTTP(rrSchema, httptest.NewRequest(http.MethodGet, "/api/schema", nil))
	if rrSchema.Code != http.StatusOK || !strings.Contains(rrSchema.Body.String(), "http_call") {
		t.Fatalf("schema invalid")
	}
}

func TestDeployResponseContainsReleaseID(t *testing.T) {
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer mgmt.Close()
	s, _ := NewServer("", ServerConfig{Targets: []Target{{Name: "gw-dev", Level: "dev", URLs: []string{mgmt.URL}}}})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(`{"levels":["dev"],"payload":{"sync_uuid":"abc"}}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy failed: %d", rr.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(body["release_id"]) == 0 {
		t.Fatalf("release_id missing")
	}
}

func TestOpenAPIImportYAMLFlexibleIndentation(t *testing.T) {
	s, _ := NewServer("http://127.0.0.1:8081", ServerConfig{})
	yamlSpec := "openapi: 3.0.0\npaths:\n    /alpha:\n      get:\n        operationId: getAlpha\n"
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/openapi/import", strings.NewReader(`{"spec":`+strconv.Quote(yamlSpec)+`}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "getAlpha") {
		t.Fatalf("yaml import with flexible indentation failed: %d %s", rr.Code, rr.Body.String())
	}
}

func TestNewServerRejectsMalformedTargetURL(t *testing.T) {
	_, err := NewServer("", ServerConfig{Targets: []Target{{Name: "bad", Level: "dev", URLs: []string{":"}}}})
	if err == nil {
		t.Fatalf("expected malformed target URL error")
	}
}

func TestDeployPreservesTargetPathPrefix(t *testing.T) {
	seenPath := ""
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer mgmt.Close()

	baseWithPrefix := mgmt.URL + "/gw"
	s, err := NewServer("", ServerConfig{Targets: []Target{{Name: "gw", Level: "dev", URLs: []string{baseWithPrefix}}}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(`{"levels":["dev"],"payload":{"sync_uuid":"abc"}}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy failed: %d %s", rr.Code, rr.Body.String())
	}
	if seenPath != "/gw/sync" {
		t.Fatalf("expected /gw/sync path, got %s", seenPath)
	}
}

func TestProxyPreservesPathPrefix(t *testing.T) {
	seenPath := ""
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"apis":[]}`))
	}))
	defer mgmt.Close()

	s, err := NewServer(mgmt.URL+"/gw", ServerConfig{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/getAllApis", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("proxy failed: %d", rr.Code)
	}
	if seenPath != "/gw/getAllApis" {
		t.Fatalf("expected /gw/getAllApis path, got %s", seenPath)
	}
}

func TestSyncProxySandboxRouting(t *testing.T) {
	const syncBody = `{"sync_uuid":"test-123","flows":[],"apis":[]}`

	t.Run("no sandbox configured routes to default", func(t *testing.T) {
		seenPath := ""
		mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer mgmt.Close()

		s, err := NewServer(mgmt.URL, ServerConfig{})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(syncBody)))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if seenPath != "/sync" {
			t.Fatalf("expected default gateway to receive /sync, got %s", seenPath)
		}
	})

	t.Run("sandbox configured routes to sandbox target only", func(t *testing.T) {
		var defaultCalled, sandboxCalled bool

		defaultGW := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defaultCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		defer defaultGW.Close()

		sandboxGW := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sandboxCalled = true
			if r.URL.Path != "/sync" {
				t.Errorf("sandbox expected /sync, got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer sandboxGW.Close()

		s, err := NewServer(defaultGW.URL, ServerConfig{
			SandboxDeployment: "dev-sandbox",
			Deployments: []Deployment{
				{Name: "dev-sandbox", Targets: []string{sandboxGW.URL}},
			},
		})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(syncBody)))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if !sandboxCalled {
			t.Fatal("sandbox gateway was not called")
		}
		if defaultCalled {
			t.Fatal("default gateway should not have been called when sandbox is configured")
		}
	})

	t.Run("sandbox deployment name not in config returns 500", func(t *testing.T) {
		dummy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer dummy.Close()
		s, err := NewServer(dummy.URL, ServerConfig{
			SandboxDeployment: "missing-sandbox",
			Deployments:       []Deployment{{Name: "other", Targets: []string{dummy.URL}}},
		})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(syncBody)))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "missing-sandbox") {
			t.Fatalf("error message should mention deployment name, got: %s", rr.Body.String())
		}
	})

	t.Run("sandbox deployment with no targets returns 500", func(t *testing.T) {
		dummy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer dummy.Close()
		s, err := NewServer(dummy.URL, ServerConfig{
			SandboxDeployment: "empty-sandbox",
			Deployments:       []Deployment{{Name: "empty-sandbox", Targets: []string{}}},
		})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(syncBody)))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rr.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		dummy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer dummy.Close()
		s, err := NewServer(dummy.URL, ServerConfig{})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/sync", nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rr.Code)
		}
	})
}
