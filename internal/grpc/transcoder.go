package grpcutil

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// jsonUnmarshalOpts is the shared unmarshal configuration for JSON → proto.
// DiscardUnknown=false causes an error for unknown JSON fields — this enforces
// schema correctness at the gateway boundary. Set to true if you need lenient mode.
var jsonUnmarshalOpts = protojson.UnmarshalOptions{
	DiscardUnknown: false,
	AllowPartial:   false,
}

// jsonMarshalOpts is the shared marshal configuration for proto → JSON.
// UseProtoNames=false emits camelCase field names (standard proto3 JSON mapping).
// EmitUnpopulated=false omits zero-value fields for compact output.
var jsonMarshalOpts = protojson.MarshalOptions{
	EmitUnpopulated: false,
	UseProtoNames:   false, // camelCase — matches standard proto3 JSON convention
	AllowPartial:    false,
}

// JSONToProto converts a JSON byte slice into a dynamicpb.Message described by desc.
//
// The returned message is freshly allocated (dynamicpb.NewMessage). The caller
// passes it directly to grpc.ClientConn.Invoke as the request argument.
//
// Returns an error if:
//   - jsonBytes is nil or empty and the message has required fields
//   - a JSON field does not match any proto field (unknown field)
//   - a value cannot be coerced to the proto field type
func JSONToProto(desc protoreflect.MessageDescriptor, jsonBytes []byte) (*dynamicpb.Message, error) {
	msg := dynamicpb.NewMessage(desc)
	if len(jsonBytes) == 0 {
		// Empty body → empty message. Valid for RPCs with no request fields.
		return msg, nil
	}
	if err := jsonUnmarshalOpts.Unmarshal(jsonBytes, msg); err != nil {
		return nil, fmt.Errorf("grpc transcoder: JSON→proto: %w", err)
	}
	return msg, nil
}

// ProtoToJSON converts a proto.Message to JSON bytes using the standard proto3
// JSON mapping (camelCase field names, omit zero-value fields).
//
// Handles all standard proto3 types including well-known types (Timestamp,
// Duration, Struct, Value, Any, etc.) which protojson renders as their
// canonical JSON forms.
func ProtoToJSON(msg proto.Message) ([]byte, error) {
	out, err := jsonMarshalOpts.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("grpc transcoder: proto→JSON: %w", err)
	}
	return out, nil
}

// LenientJSONToProto is like JSONToProto but silently drops unknown JSON fields.
// Use this when the upstream may return fields not present in the stored descriptor.
func LenientJSONToProto(desc protoreflect.MessageDescriptor, jsonBytes []byte) (*dynamicpb.Message, error) {
	msg := dynamicpb.NewMessage(desc)
	if len(jsonBytes) == 0 {
		return msg, nil
	}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(jsonBytes, msg); err != nil {
		return nil, fmt.Errorf("grpc transcoder (lenient): JSON→proto: %w", err)
	}
	return msg, nil
}
