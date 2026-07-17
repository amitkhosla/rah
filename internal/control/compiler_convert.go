package control

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/amitkhosla/rah/internal/avro"
	"github.com/amitkhosla/rah/internal/engine/steps"
	grpcutil "github.com/amitkhosla/rah/internal/grpc"
)

// compileAvroToProto handles the "avro_to_proto" step type.
// Multi-hop: Avro binary â†’ JSON (pivot) â†’ Proto binary.
//
// Step input keys:
//
//	src_slot        â€” var name whose ByteSlot contains Avro binary (required)
//	dst_slot        â€” var name whose ByteSlot will receive Proto binary (required)
//	avro_schema     â€” Avro JSON schema string for the source (required)
//	descriptor_set  â€” name of the registered FileDescriptorSet (required)
//	message         â€” fully-qualified Proto message name (required)
func (c *Compiler) compileAvroToProto(step StepConfig) error {
	schemaJSON := step.Input["avro_schema"]
	if schemaJSON == "" {
		return fmt.Errorf("avro_to_proto: 'avro_schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("avro_to_proto: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_proto: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("avro_to_proto: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_proto: dst_slot: %w", err)
	}

	// Compile Avro decoder program
	decProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("avro_to_proto: avro schema compile: %w", err)
	}

	// Resolve proto message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("avro_to_proto: 'message' is required")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			msgDesc, err = registry.FindMessage(setName, msgName)
			if err != nil {
				return fmt.Errorf("avro_to_proto: %w", err)
			}
		}
	}
	if msgDesc == nil {
		return fmt.Errorf("avro_to_proto: unable to resolve message %q (descriptor_set=%q)", msgName, setName)
	}

	instr, err := steps.NewAvroToProtoStep(srcSlot, dstSlot, decProg, msgDesc)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileProtoToAvro handles the "proto_to_avro" step type.
// Multi-hop: Proto binary â†’ JSON (pivot) â†’ Avro binary.
//
// Step input keys:
//
//	src_slot        â€” var name whose ByteSlot contains Proto binary (required)
//	dst_slot        â€” var name whose ByteSlot will receive Avro binary (required)
//	descriptor_set  â€” name of the registered FileDescriptorSet (required)
//	message         â€” fully-qualified Proto message name (required)
//	avro_schema     â€” Avro JSON schema string for the destination (required)
func (c *Compiler) compileProtoToAvro(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("proto_to_avro: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("proto_to_avro: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("proto_to_avro: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("proto_to_avro: dst_slot: %w", err)
	}

	// Resolve proto message descriptor
	setName := strings.TrimSpace(step.Input["descriptor_set"])
	msgName := strings.TrimSpace(step.Input["message"])
	if msgName == "" {
		return fmt.Errorf("proto_to_avro: 'message' is required")
	}

	var msgDesc protoreflect.MessageDescriptor
	if setName != "" {
		registry := c.GrpcRegistry
		if registry == nil {
			registry = steps.GlobalGrpcRegistry
		}
		if registry != nil {
			msgDesc, err = registry.FindMessage(setName, msgName)
			if err != nil {
				return fmt.Errorf("proto_to_avro: %w", err)
			}
		}
	}
	if msgDesc == nil {
		return fmt.Errorf("proto_to_avro: unable to resolve message %q (descriptor_set=%q)", msgName, setName)
	}

	// Compile Avro encoder program
	schemaJSON := step.Input["avro_schema"]
	if schemaJSON == "" {
		return fmt.Errorf("proto_to_avro: 'avro_schema' is required")
	}
	encProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("proto_to_avro: avro schema compile: %w", err)
	}

	instr, err := steps.NewProtoToAvroStep(srcSlot, dstSlot, msgDesc, encProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// resolveProtoDescriptor is a helper shared by convert compiler methods to look up
// a protoreflect.MessageDescriptor from the compiler's GrpcRegistry.
// Kept package-private; callers that already inline the lookup don't need this.
func (c *Compiler) resolveProtoDescriptor(stepType, setName, msgName string) (protoreflect.MessageDescriptor, *grpcutil.ProtoMsgPool, *sync.Pool, error) {
	registry := c.GrpcRegistry
	if registry == nil {
		registry = steps.GlobalGrpcRegistry
	}
	if registry == nil || setName == "" {
		return nil, nil, nil, fmt.Errorf("%s: unable to resolve message %q: no descriptor registry available", stepType, msgName)
	}
	msgDesc, err := registry.FindMessage(setName, msgName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", stepType, err)
	}
	msgPool := grpcutil.NewProtoMsgPool(msgDesc)
	marshalPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 0, 4096)
			return &buf
		},
	}
	return msgDesc, msgPool, marshalPool, nil
}
