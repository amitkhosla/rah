package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// A2AServer implements the Agent-to-Agent (A2A) JSON-RPC 2.0 server.
// It handles tasks/send and tasks/get methods. For streaming requests
// (Accept: text/event-stream), tasks/send results are delivered as SSE.
type A2AServer struct {
	Card    AgentCard
	Send    func(ctx context.Context, params TaskSendParams) (*Task, error)
	GetTask func(ctx context.Context, id string) (*Task, error)
}

func (s *A2AServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request")
		return
	}

	switch req.Method {
	case "tasks/send":
		s.handleTaskSend(w, r, req)
	case "tasks/get":
		s.handleTaskGet(w, r, req)
	default:
		writeRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *A2AServer) handleTaskSend(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	var params TaskSendParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid params")
			return
		}
	}

	if s.Send == nil {
		writeRPCError(w, req.ID, -32603, "tasks/send not configured")
		return
	}

	task, err := s.Send(r.Context(), params)
	if err != nil {
		writeRPCError(w, req.ID, -32603, err.Error())
		return
	}

	if r.Header.Get("Accept") == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		data, _ := json.Marshal(task)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		return
	}

	writeRPCResult(w, req.ID, task)
}

func (s *A2AServer) handleTaskGet(w http.ResponseWriter, r *http.Request, req JSONRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid params")
			return
		}
	}

	if s.GetTask == nil {
		writeRPCError(w, req.ID, -32603, "tasks/get not configured")
		return
	}

	task, err := s.GetTask(r.Context(), params.ID)
	if err != nil {
		writeRPCError(w, req.ID, -32603, err.Error())
		return
	}

	writeRPCResult(w, req.ID, task)
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
