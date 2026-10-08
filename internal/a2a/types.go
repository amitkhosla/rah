package a2a

import "encoding/json"

// AgentCard is served at /.well-known/agent.json per the A2A specification.
type AgentCard struct {
	Name         string       `json:"name"`
	Description  string       `json:"description,omitempty"`
	URL          string       `json:"url"`
	Version      string       `json:"version"`
	Capabilities Capabilities `json:"capabilities"`
	Skills       []Skill      `json:"skills,omitempty"`
}

// Capabilities declares what optional features this agent supports.
type Capabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

// Skill is one unit of capability advertised on the AgentCard.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// JSONRPCRequest is the JSON-RPC 2.0 request envelope.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is the JSON-RPC 2.0 response envelope.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError is the error object inside a JSON-RPC 2.0 error response.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// TaskSendParams holds the parameters for the tasks/send JSON-RPC method.
type TaskSendParams struct {
	ID      string          `json:"id"`
	Message json.RawMessage `json:"message"`
}

// Task is the A2A task object returned by tasks/send and tasks/get.
type Task struct {
	ID     string     `json:"id"`
	Status TaskStatus `json:"status"`
	Result any `json:"result,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// TaskStatus holds the execution state of a task.
type TaskStatus struct {
	State string `json:"state"` // "submitted" | "working" | "completed" | "failed"
}
