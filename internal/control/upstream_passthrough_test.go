package control

import (
	"net/http"
	"testing"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestResolveUpstreamPassthrough_AllNilReturnsDefaults(t *testing.T) {
	step := StepConfig{}
	forwardHeaders, forwardRespHeaders, forwardQuery, forwardPath, blockMap, txIDHeader := resolveUpstreamPassthrough(step, nil, nil)

	if forwardHeaders {
		t.Errorf("expected forwardHeaders=false, got true")
	}
	if forwardRespHeaders {
		t.Errorf("expected forwardRespHeaders=false, got true")
	}
	if forwardQuery {
		t.Errorf("expected forwardQuery=false, got true")
	}
	if forwardPath {
		t.Errorf("expected forwardPath=false, got true")
	}
	if blockMap != nil {
		t.Errorf("expected blockMap=nil, got %v", blockMap)
	}
	if txIDHeader != "" {
		t.Errorf("expected txIDHeader=\"\", got %q", txIDHeader)
	}
}

func TestResolveUpstreamPassthrough_GatewayDefaultsApply(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(true),
		ForwardQueryParams:     boolPtr(true),
	}
	step := StepConfig{}
	forwardHeaders, _, forwardQuery, _, _, _ := resolveUpstreamPassthrough(step, nil, gwDefaults)

	if !forwardHeaders {
		t.Errorf("expected forwardHeaders=true from gateway, got false")
	}
	if !forwardQuery {
		t.Errorf("expected forwardQuery=true from gateway, got false")
	}
}

func TestResolveUpstreamPassthrough_APIOverridesGateway(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		ForwardQueryParams: boolPtr(true),
	}
	apiDefaults := &UpstreamPassthroughConfig{
		ForwardQueryParams: boolPtr(false),
	}
	step := StepConfig{}
	_, _, forwardQuery, _, _, _ := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if forwardQuery {
		t.Errorf("expected forwardQuery=false from API override, got true")
	}
}

func TestResolveUpstreamPassthrough_StepOverridesAll(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(true),
	}
	apiDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(false),
	}
	step := StepConfig{
		Upstream: &UpstreamPassthroughConfig{
			ForwardIncomingHeaders: boolPtr(true),
		},
	}
	forwardHeaders, _, _, _, _, _ := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if !forwardHeaders {
		t.Errorf("expected forwardHeaders=true from step, got false")
	}
}

func TestResolveUpstreamPassthrough_LegacyFlatFieldsWork(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(false),
	}
	step := StepConfig{
		ForwardIncomingHeaders: true,
		Upstream:               nil,
	}
	forwardHeaders, _, _, _, _, _ := resolveUpstreamPassthrough(step, nil, gwDefaults)

	if !forwardHeaders {
		t.Errorf("expected forwardHeaders=true from legacy flat field, got false")
	}
}

func TestResolveUpstreamPassthrough_BlockHeadersCascade(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		BlockHeaders: []string{"Authorization"},
	}
	apiDefaults := &UpstreamPassthroughConfig{
		BlockHeaders: nil, // inherit from gateway
	}
	step := StepConfig{
		Upstream: &UpstreamPassthroughConfig{
			BlockHeaders: []string{}, // non-nil empty replaces
		},
	}
	_, _, _, _, blockMap, _ := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if blockMap != nil {
		t.Errorf("expected blockMap=nil when step sets empty slice, got %v", blockMap)
	}
}

func TestResolveUpstreamPassthrough_TxIDHeaderCascade(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		InjectTxIDHeader: "X-Request-ID",
	}
	apiDefaults := &UpstreamPassthroughConfig{
		InjectTxIDHeader: "", // inherit from gateway
	}
	step := StepConfig{
		Upstream: &UpstreamPassthroughConfig{
			InjectTxIDHeader: "-", // explicitly disable
		},
	}
	_, _, _, _, _, txIDHeader := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if txIDHeader != "" {
		t.Errorf("expected txIDHeader=\"\" when step disables with \"-\", got %q", txIDHeader)
	}
}

func TestResolveUpstreamPassthrough_TxIDHeaderCanonical(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		InjectTxIDHeader: "x-request-id", // lowercase
	}
	_, _, _, _, _, txIDHeader := resolveUpstreamPassthrough(StepConfig{}, nil, gwDefaults)

	// http.CanonicalHeaderKey should canonicalize it
	if txIDHeader != "X-Request-Id" {
		t.Errorf("expected txIDHeader=\"X-Request-Id\", got %q", txIDHeader)
	}
}

func TestResolveUpstreamPassthrough_BlockHeadersInheritThenReplace(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		BlockHeaders: []string{"Authorization", "Cookie"},
	}
	apiDefaults := &UpstreamPassthroughConfig{
		BlockHeaders: nil, // inherit from gateway
	}
	step := StepConfig{
		Upstream: &UpstreamPassthroughConfig{
			BlockHeaders: nil, // inherit from API → inherit from gateway
		},
	}
	_, _, _, _, blockMap, _ := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if blockMap == nil {
		t.Fatalf("expected blockMap to be set, got nil")
	}
	// Check canonical forms
	authKey := http.CanonicalHeaderKey("Authorization")
	cookieKey := http.CanonicalHeaderKey("Cookie")
	if _, ok := blockMap[authKey]; !ok {
		t.Errorf("expected blockMap to contain %q", authKey)
	}
	if _, ok := blockMap[cookieKey]; !ok {
		t.Errorf("expected blockMap to contain %q", cookieKey)
	}
	if len(blockMap) != 2 {
		t.Errorf("expected blockMap to have 2 entries, got %d", len(blockMap))
	}
}

func TestResolveUpstreamPassthrough_LegacyFlatFieldsTakesPriorityOverAPI(t *testing.T) {
	apiDefaults := &UpstreamPassthroughConfig{
		ForwardResponseHeaders: boolPtr(false),
	}
	step := StepConfig{
		ForwardResponseHeaders: true,
		Upstream:               nil, // legacy flat field mode
	}
	_, forwardRespHeaders, _, _, _, _ := resolveUpstreamPassthrough(step, apiDefaults, nil)

	if !forwardRespHeaders {
		t.Errorf("expected forwardRespHeaders=true from legacy flat field, got false")
	}
}

func TestResolveUpstreamPassthrough_AllFieldsCombined(t *testing.T) {
	gwDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(false),
		ForwardQueryParams:     boolPtr(false),
		BlockHeaders:           []string{"X-Internal"},
		InjectTxIDHeader:       "X-Correlation-ID",
	}
	apiDefaults := &UpstreamPassthroughConfig{
		ForwardIncomingHeaders: boolPtr(true),
		BlockHeaders:           []string{}, // empty overrides
	}
	step := StepConfig{
		Upstream: &UpstreamPassthroughConfig{
			ForwardQueryParams:  boolPtr(true),
			ForwardPathSuffix:   boolPtr(true),
			InjectTxIDHeader:    "x-tx-id", // lowercase, should be canonical
		},
	}

	forwardHeaders, _, forwardQuery, forwardPath, blockMap, txIDHeader := resolveUpstreamPassthrough(step, apiDefaults, gwDefaults)

	if !forwardHeaders {
		t.Errorf("expected forwardHeaders=true from API, got false")
	}
	if !forwardQuery {
		t.Errorf("expected forwardQuery=true from step, got false")
	}
	if !forwardPath {
		t.Errorf("expected forwardPath=true from step, got false")
	}
	if blockMap != nil {
		t.Errorf("expected blockMap=nil from API override with empty slice, got %v", blockMap)
	}
	if txIDHeader != "X-Tx-Id" {
		t.Errorf("expected txIDHeader=\"X-Tx-Id\", got %q", txIDHeader)
	}
}
