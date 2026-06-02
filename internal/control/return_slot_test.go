package control

// return_slot_test.go

import (
	"testing"
)

func TestReturnWithSlotRefViaSync(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	sendOkResult, err := ParseDSL("return(200, response_body)")
	if err != nil {
		t.Fatal("parse send_ok:", err)
	}
	buildHelloResult, err := ParseDSL("response_body = '{\"message\":\"hello, world!\"}'")
	if err != nil {
		t.Fatal("parse build_hello:", err)
	}
	apiHelloResult, err := ParseDSL("call build_hello_payload\ncall send_ok")
	if err != nil {
		t.Fatal("parse api_hello:", err)
	}

	if len(sendOkResult.Steps) == 0 {
		t.Fatal("send_ok has no steps")
	}
	retStep := sendOkResult.Steps[0]
	if retStep.Body != "" {
		t.Errorf("send_ok return Body=%q want empty (must use As for slot)", retStep.Body)
	}
	if retStep.As != "response_body" {
		t.Errorf("send_ok return As=%q want response_body", retStep.As)
	}

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "hello-slot-test",
		Flows: []FlowUpdate{
			{Name: "send_ok", Instructions: sendOkResult.Steps, Action: "upsert"},
			{Name: "build_hello_payload", Instructions: buildHelloResult.Steps, Action: "upsert"},
			{Name: "api_hello", Instructions: apiHelloResult.Steps, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{
				Name:          "sample-cs-hello",
				Path:          "/samples/cs/static/hello",
				Method:        "GET",
				FlowName:      "api_hello",
				Action:        "upsert",
				SkipRateLimit: true,
			},
		},
	})

	ctx := runRequest(t, fm, "GET", "/samples/cs/static/hello", nil)
	body := string(ctx.ResponseBuffer)
	t.Logf("ResponseStatus=%d Body=%q", ctx.ResponseStatus, body)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected status 200, got %d", ctx.ResponseStatus)
	}
	const want = `{"message":"hello, world!"}`
	if body != want {
		t.Errorf("expected body %q, got %q", want, body)
	}
}
