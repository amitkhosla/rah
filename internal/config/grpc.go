package config

// GrpcConfig holds gateway-wide defaults for gRPC egress connections.
// All fields are optional; zero values apply reasonable defaults.
type GrpcConfig struct {
	// MaxRecvMsgSizeMB is the maximum gRPC response message size in megabytes.
	// 0 = use gRPC library default (4 MB).
	MaxRecvMsgSizeMB int `json:"max_recv_msg_size_mb,omitempty" yaml:"max_recv_msg_size_mb,omitempty"`

	// MaxSendMsgSizeMB is the maximum gRPC request message size in megabytes.
	// 0 = use gRPC library default (unlimited on client side).
	MaxSendMsgSizeMB int `json:"max_send_msg_size_mb,omitempty" yaml:"max_send_msg_size_mb,omitempty"`

	// KeepaliveTimeSec is how often to send keep-alive PING frames (seconds).
	// 0 = disabled. Recommended: 30 for cross-datacenter connections.
	KeepaliveTimeSec int `json:"keepalive_time_sec,omitempty" yaml:"keepalive_time_sec,omitempty"`

	// KeepaliveTimeoutSec is how long to wait for a PING ack before closing.
	// 0 = use gRPC default (20s). Only used when KeepaliveTimeSec > 0.
	KeepaliveTimeoutSec int `json:"keepalive_timeout_sec,omitempty" yaml:"keepalive_timeout_sec,omitempty"`

	// WaitForReadyDefault sets the default wait-for-ready behaviour for all grpc_call
	// steps that do not explicitly specify wait_for_ready. Default: false (fail fast).
	WaitForReadyDefault bool `json:"wait_for_ready_default,omitempty" yaml:"wait_for_ready_default,omitempty"`
}
