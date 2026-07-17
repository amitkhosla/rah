package control

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/amitkhosla/rah/internal/engine/steps"
	grpcutil "github.com/amitkhosla/rah/internal/grpc"
)

// compileProtoToJSON compiles a "proto_to_json" step.
// Converts proto binary to JSON.
func (c *Compiler) compileProtoToJSON(step StepConfig) error {
	srcSlot, err := c.getSlot(step.Input["source"])
	if err != nil {
		return fmt.Errorf("proto_to_json source: %w", err)
	}
	dstSlot, err := c.getSlot(step.Input["as"])
	if err != nil {
		return fmt.Errorf("proto_to_json as: %w", err)
	}

	// Resolve message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("proto_to_json: missing required field 'message'")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			var errMsg error
			msgDesc, errMsg = registry.FindMessage(setName, msgName)
			if errMsg != nil {
				return fmt.Errorf("proto_to_json: %w", errMsg)
			}
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("proto_to_json: unable to resolve message %q", msgName)
	}

	cfg := steps.ProtoToJSONConfig{
		SrcSlot: srcSlot,
		DstSlot: dstSlot,
		MsgDesc: msgDesc,
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewProtoToJSONInstruction(cfg))
	return nil
}

// compileJSONToProto compiles a "json_to_proto" step.
// Converts JSON to proto binary with pooled message and buffer management.
func (c *Compiler) compileJSONToProto(step StepConfig) error {
	srcSlot, err := c.getSlot(step.Input["source"])
	if err != nil {
		return fmt.Errorf("json_to_proto source: %w", err)
	}
	dstSlot, err := c.getSlot(step.Input["as"])
	if err != nil {
		return fmt.Errorf("json_to_proto as: %w", err)
	}

	// Resolve message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("json_to_proto: missing required field 'message'")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			var errMsg error
			msgDesc, errMsg = registry.FindMessage(setName, msgName)
			if errMsg != nil {
				return fmt.Errorf("json_to_proto: %w", errMsg)
			}
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("json_to_proto: unable to resolve message %q", msgName)
	}

	// Create or reuse pool for this message descriptor
	msgPool := grpcutil.NewProtoMsgPool(msgDesc)

	// Marshal buffer pool (sync.Pool of *[]byte)
	marshalBufPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 0, 4096)
			return &buf
		},
	}

	cfg := steps.JSONToProtoConfig{
		SrcSlot:        srcSlot,
		DstSlot:        dstSlot,
		MsgDesc:        msgDesc,
		MsgPool:        msgPool,
		MarshalBufPool: marshalBufPool,
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewJSONToProtoInstruction(cfg))
	return nil
}

// compileProtoGet compiles a "proto_get" step.
// Extracts a field from proto binary (static or dynamic path).
func (c *Compiler) compileProtoGet(step StepConfig) error {
	srcSlot, err := c.getSlot(step.Input["source"])
	if err != nil {
		return fmt.Errorf("proto_get source: %w", err)
	}
	dstSlot, err := c.getSlot(step.Input["as"])
	if err != nil {
		return fmt.Errorf("proto_get as: %w", err)
	}

	// Resolve message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("proto_get: missing required field 'message'")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			var errMsg error
			msgDesc, errMsg = registry.FindMessage(setName, msgName)
			if errMsg != nil {
				return fmt.Errorf("proto_get: %w", errMsg)
			}
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("proto_get: unable to resolve message %q", msgName)
	}

	// Field path can be static or from a slot
	staticPath := strings.TrimSpace(step.Input["path"])
	pathSlot := -1
	if pathVarName := strings.TrimSpace(step.Input["path_var"]); pathVarName != "" {
		s, err := c.getSlot(pathVarName)
		if err != nil {
			return fmt.Errorf("proto_get path_var: %w", err)
		}
		pathSlot = s
	}

	if staticPath == "" && pathSlot < 0 {
		return fmt.Errorf("proto_get: one of 'path' or 'path_var' is required")
	}

	// Message pool
	msgPool := grpcutil.NewProtoMsgPool(msgDesc)

	// JSON buffer pool for dynamic paths
	jsonBufPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 0, 4096)
			return &buf
		},
	}

	cfg := steps.ProtoGetConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		StaticPath:  staticPath,
		PathSlot:    pathSlot,
		MsgDesc:     msgDesc,
		MsgPool:     msgPool,
		JSONBufPool: jsonBufPool,
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewProtoGetInstruction(cfg))
	return nil
}

// compileXMLToProto compiles an "xml_to_proto" step (multi-hop).
func (c *Compiler) compileXMLToProto(step StepConfig) error {
	srcSlot, err := c.getSlot(step.Input["source"])
	if err != nil {
		return fmt.Errorf("xml_to_proto source: %w", err)
	}
	dstSlot, err := c.getSlot(step.Input["as"])
	if err != nil {
		return fmt.Errorf("xml_to_proto as: %w", err)
	}

	// Resolve message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("xml_to_proto: missing required field 'message'")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			var errMsg error
			msgDesc, errMsg = registry.FindMessage(setName, msgName)
			if errMsg != nil {
				return fmt.Errorf("xml_to_proto: %w", errMsg)
			}
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("xml_to_proto: unable to resolve message %q", msgName)
	}

	msgPool := grpcutil.NewProtoMsgPool(msgDesc)

	marshalBufPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 0, 4096)
			return &buf
		},
	}

	cfg := steps.XMLToProtoConfig{
		SrcSlot:        srcSlot,
		DstSlot:        dstSlot,
		MsgDesc:        msgDesc,
		MsgPool:        msgPool,
		MarshalBufPool: marshalBufPool,
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewXMLToProtoInstruction(cfg))
	return nil
}

// compileProtoToXML compiles a "proto_to_xml" step (multi-hop).
func (c *Compiler) compileProtoToXML(step StepConfig) error {
	srcSlot, err := c.getSlot(step.Input["source"])
	if err != nil {
		return fmt.Errorf("proto_to_xml source: %w", err)
	}
	dstSlot, err := c.getSlot(step.Input["as"])
	if err != nil {
		return fmt.Errorf("proto_to_xml as: %w", err)
	}

	// Resolve message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("proto_to_xml: missing required field 'message'")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			var errMsg error
			msgDesc, errMsg = registry.FindMessage(setName, msgName)
			if errMsg != nil {
				return fmt.Errorf("proto_to_xml: %w", errMsg)
			}
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("proto_to_xml: unable to resolve message %q", msgName)
	}

	cfg := steps.ProtoToXMLConfig{
		SrcSlot: srcSlot,
		DstSlot: dstSlot,
		MsgDesc: msgDesc,
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewProtoToXMLInstruction(cfg))
	return nil
}
