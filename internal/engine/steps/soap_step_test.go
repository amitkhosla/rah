package steps

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/soap"
	"testing"
	"time"
)

func TestSOAPCall_BuildsEnvelope(t *testing.T) {
	// Setup mock HTTP server that echoes back the request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Wrap request in SOAP response
		respEnv := soap.AppendSOAP11Envelope(nil, body)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write(respEnv)
	}))
	defer server.Close()

	cfg := SOAPCallConfig{
		StaticURL:           server.URL,
		URLSlot:             -1,
		BodySlot:            0,
		ResponseBodySlot:    1,
		ResponseStatusSlot:  2,
		ContentTypeSlot:     -1,
		StaticContentType:   "text/xml",
		MaxRetries:          0,
		FlowInput:           make(map[string]string),
		soapVersion:         1,
	}

	instr := SOAPCallFromConfig(cfg)
	if instr.Name != "SOAP_CALL" {
		t.Errorf("expected instruction name SOAP_CALL, got %s", instr.Name)
	}

	// Create context and execute
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<user><name>Alice</name></user>`)

	state := &engine.ExecutionState{PC: 0}
	pc := instr.Action(ctx, state)
	if pc != 1 {
		t.Errorf("expected PC+1=1, got %d", pc)
	}

	// Check that response was stored
	if len(ctx.ByteSlots[1]) == 0 {
		t.Errorf("expected response body in slot 1")
	}
	if ctx.IntSlots[2] != http.StatusOK {
		t.Errorf("expected status 200, got %d", ctx.IntSlots[2])
	}
}

func TestSOAPCall_ParsesFault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fault := []byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body>
<soapenv:Fault>
<faultcode>Server</faultcode>
<faultstring>Service unavailable</faultstring>
</soapenv:Fault>
</soapenv:Body>
</soapenv:Envelope>`)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(fault)
	}))
	defer server.Close()

	cfg := SOAPCallConfig{
		StaticURL:           server.URL,
		URLSlot:             -1,
		BodySlot:            0,
		ResponseBodySlot:    1,
		ResponseStatusSlot:  2,
		ContentTypeSlot:     -1,
		StaticContentType:   "text/xml",
		MaxRetries:          0,
		FlowInput:           make(map[string]string),
		soapVersion:         1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<request/>`)

	_ = instr.Action(ctx, nil)
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true for fault response")
	}
	if ctx.ResponseStatus != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, ctx.ResponseStatus)
	}
}

func TestSOAPCall_MissingBody(t *testing.T) {
	cfg := SOAPCallConfig{
		StaticURL:          "http://localhost:9999",
		URLSlot:            -1,
		BodySlot:           0,
		ResponseBodySlot:   1,
		ResponseStatusSlot: 2,
		MaxRetries:         0,
		FlowInput:          make(map[string]string),
		soapVersion:        1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	// Don't set ByteSlots[0] â€” leave it nil/empty

	state := &engine.ExecutionState{PC: 0}
	pc := instr.Action(ctx, state)
	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", pc)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true for missing body")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("expected status 400, got %d", ctx.ResponseStatus)
	}
}

func TestSOAPCall_EmptyURL(t *testing.T) {
	cfg := SOAPCallConfig{
		StaticURL:          "",
		URLSlot:            -1,
		BodySlot:           0,
		ResponseBodySlot:   1,
		ResponseStatusSlot: 2,
		MaxRetries:         0,
		FlowInput:          make(map[string]string),
		soapVersion:        1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<request/>`)

	state := &engine.ExecutionState{PC: 0}
	pc := instr.Action(ctx, state)
	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", pc)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true for empty URL")
	}
}

func TestSOAPCall_TimeoutContext(t *testing.T) {
	// Create a slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := SOAPCallConfig{
		StaticURL:           server.URL,
		URLSlot:             -1,
		BodySlot:            0,
		ResponseBodySlot:    1,
		ResponseStatusSlot:  2,
		Timeout:             100, // 100ms timeout (short)
		MaxRetries:          0,
		FlowInput:           make(map[string]string),
		soapVersion:         1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<request/>`)

	pc := instr.Action(ctx, nil)
	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan due to timeout")
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true for timeout")
	}
}

func TestSOAPCall_Concurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		respEnv := soap.AppendSOAP11Envelope(nil, body)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write(respEnv)
	}))
	defer server.Close()

	cfg := SOAPCallConfig{
		StaticURL:           server.URL,
		URLSlot:             -1,
		BodySlot:            0,
		ResponseBodySlot:    1,
		ResponseStatusSlot:  2,
		MaxRetries:          0,
		FlowInput:           make(map[string]string),
		soapVersion:         1,
	}

	instr := SOAPCallFromConfig(cfg)

	// Run 8 concurrent requests
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- true }()

			ctx := &rctx.Context{
				ByteSlots: make([][]byte, 4),
				IntSlots:  make([]int64, 4),
				BoolSlots: make([]bool, 4),
			}
			ctx.ByteSlots[0] = []byte(`<user><name>Test</name></user>`)

			state := &engine.ExecutionState{PC: 0}
			pc := instr.Action(ctx, state)
			if pc == engine.StopPlan {
				t.Errorf("unexpected StopPlan, got %d", pc)
			}
			if len(ctx.ByteSlots[1]) == 0 {
				t.Errorf("expected response body")
			}
		}()
	}

	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestSOAPCallFromConfig_StaticURLValidation(t *testing.T) {
	cfg := SOAPCallConfig{
		StaticURL:    "not-a-url",
		URLSlot:      -1,
		URLPolicy:    URLPolicyStrict,
		BodySlot:     0,
		FlowInput:    make(map[string]string),
		soapVersion:  1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<request/>`)

	pc := instr.Action(ctx, nil)
	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan for invalid URL")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
}

// TestSOAPCall_ConnectError simulates a connection error
func TestSOAPCall_ConnectError(t *testing.T) {
	// Use a port that's very likely not listening
	cfg := SOAPCallConfig{
		StaticURL:           "http://127.0.0.1:1",
		URLSlot:             -1,
		BodySlot:            0,
		ResponseBodySlot:    1,
		ResponseStatusSlot:  2,
		Timeout:             100, // short timeout
		MaxRetries:          0,
		FlowInput:           make(map[string]string),
		soapVersion:         1,
	}

	instr := SOAPCallFromConfig(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
		IntSlots:  make([]int64, 4),
		BoolSlots: make([]bool, 4),
	}
	ctx.ByteSlots[0] = []byte(`<request/>`)

	pc := instr.Action(ctx, nil)
	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan on connection error")
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
}

// Helper to check if httptest server is available
func isNetworkAvailable(t *testing.T) bool {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network not available: %v", err)
		return false
	}
	if err := listener.Close(); err != nil {
		t.Logf("listener close: %v", err)
	}
	return true
}
