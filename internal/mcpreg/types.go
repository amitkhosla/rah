package mcpreg

import "encoding/json"

// ToolSourceKind identifies how a tool is sourced.
type ToolSourceKind string

const (
	ToolSourceAPI     ToolSourceKind = "api_tool"  // RAH API endpoint as tool
	ToolSourceMCPTool ToolSourceKind = "mcp_tool"  // specific tool from an MCP server
	ToolSourceMCPAll  ToolSourceKind = "mcp_all"   // all tools from an MCP server
)

// APIToolDef defines a RAH API endpoint exposed as an MCP tool.
type APIToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"` // JSON Schema object
	Path        string          `json:"path"`                   // e.g. "/v1/kb/search"
	Method      string          `json:"method"`                 // GET, POST, etc.
	// Auth for calling this tool
	AuthKind   string `json:"auth_kind,omitempty"`   // "bearer", "header", "none"
	AuthHeader string `json:"auth_header,omitempty"` // header name if kind=header
	AuthKeyRef string `json:"auth_key_ref,omitempty"` // secret ref or literal key
}

// ToolSource is one entry in a VirtualMCPServerDef's tool list.
type ToolSource struct {
	Kind ToolSourceKind `json:"kind"`
	// For kind=api_tool:
	APITool *APIToolDef `json:"api_tool,omitempty"`
	// For kind=mcp_tool:
	ServerAlias string `json:"server_alias,omitempty"` // registered MCP server alias
	ToolName    string `json:"tool_name,omitempty"`    // specific tool name
	// For kind=mcp_all:
	// ServerAlias is also used here (all tools from that server)
}

// VirtualMCPServerDef is a named, configurable virtual MCP server.
// It aggregates tools from API endpoints and/or external MCP servers.
type VirtualMCPServerDef struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	// TenantID 0 = global (available to all tenants)
	// Non-zero = scoped to that tenant
	TenantID uint16       `json:"tenant_id,omitempty"`
	Sources  []ToolSource `json:"sources"`
}
