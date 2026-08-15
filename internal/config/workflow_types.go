package config

// WorkflowNode represents a flow in the workflow DAG.
type WorkflowNode struct {
	ID       string  `json:"id"`              // unique within the workflow (e.g. "payment-validate")
	FlowName string  `json:"flow_name"`       // name of the rah flow this node represents
	Label    string  `json:"label,omitempty"` // display label (defaults to FlowName if empty)
	X        float64 `json:"x,omitempty"`     // Canvas position (design-time only)
	Y        float64 `json:"y,omitempty"`
}

// WorkflowEdge connects two nodes via an event listener.
type WorkflowEdge struct {
	ID           string `json:"id"`             // unique within the workflow
	SourceNodeID string `json:"source_node_id"` // ID of the source node
	TargetNodeID string `json:"target_node_id"` // ID of the target node
	ListenerName string `json:"listener_name"`  // event listener that fires from source → target
	Label        string `json:"label,omitempty"`
}

// WorkflowDefinition is a named DAG of flows connected by event listeners.
type WorkflowDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	AppName     string         `json:"app_name,omitempty"` // optional app this workflow belongs to
	Nodes       []WorkflowNode `json:"nodes"`
	Edges       []WorkflowEdge `json:"edges"`
	CreatedAt   int64          `json:"created_at,omitempty"` // unix seconds
	UpdatedAt   int64          `json:"updated_at,omitempty"`
}
