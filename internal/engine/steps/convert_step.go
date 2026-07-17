package steps

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/amitkhosla/rah/internal/avro"
	"github.com/amitkhosla/rah/internal/engine"
	grpcutil "github.com/amitkhosla/rah/internal/grpc"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/transcode"
)

// â”€â”€ avro_to_proto â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// AvroToProtoConvertConfig holds the state for the avro_to_proto instruction.
// Multi-hop: Avro binary â†’ JSON (pivot) â†’ Proto binary.
type AvroToProtoConvertConfig struct {
	SrcSlot        int                            // source ByteSlot (Avro binary)
	DstSlot        int                            // destination ByteSlot (Proto binary)
	DecProg        *avro.AvroProgram              // decode Avro â†’ JSON
	MsgDesc        protoreflect.MessageDescriptor // proto message descriptor
	MsgPool        *grpcutil.ProtoMsgPool         // pooled dynamic messages
	MarshalBufPool *sync.Pool                     // *[]byte pool for proto marshal scratch
}

// Action converts Avro binary to Proto binary via JSON pivot.
func (cfg *AvroToProtoConvertConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]

	// Step 1: Avro â†’ JSON into pivot buffer
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	jsonBytes, err := avro.AppendAvroToJSON(pivot.Bytes(), src, cfg.DecProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Step 2: JSON â†’ proto dynamic message
	msg := cfg.MsgPool.Get()
	defer cfg.MsgPool.Put(msg)

	if err := protojson.Unmarshal(jsonBytes, msg); err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Step 3: marshal proto message to binary
	bufV := cfg.MarshalBufPool.Get()
	buf := bufV.(*[]byte)
	defer func() {
		*buf = (*buf)[:0]
		cfg.MarshalBufPool.Put(buf)
	}()

	*buf, err = proto.MarshalOptions{}.MarshalAppend((*buf)[:0], msg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy result to arena
	out := ctx.Alloc(len(*buf))
	copy(out, *buf)
	ctx.ByteSlots[cfg.DstSlot] = out
	return state.PC + 1
}

// NewAvroToProtoStep creates an avro_to_proto instruction.
func NewAvroToProtoStep(srcSlot, dstSlot int, decProg *avro.AvroProgram,
	msgDesc protoreflect.MessageDescriptor) (engine.Instruction, error) {

	if decProg == nil {
		return engine.Instruction{}, fmt.Errorf("avro_to_proto: decProg is nil")
	}
	if msgDesc == nil {
		return engine.Instruction{}, fmt.Errorf("avro_to_proto: msgDesc is nil")
	}

	cfg := &AvroToProtoConvertConfig{
		SrcSlot: srcSlot,
		DstSlot: dstSlot,
		DecProg: decProg,
		MsgDesc: msgDesc,
		MsgPool: grpcutil.NewProtoMsgPool(msgDesc),
		MarshalBufPool: &sync.Pool{
			New: func() any {
				buf := make([]byte, 0, 4096)
				return &buf
			},
		},
	}
	return engine.Instruction{
		Name:   "AVRO_TO_PROTO",
		Action: cfg.Action,
	}, nil
}

// â”€â”€ proto_to_avro â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// ProtoToAvroConvertConfig holds the state for the proto_to_avro instruction.
// Multi-hop: Proto binary â†’ JSON (pivot) â†’ Avro binary.
type ProtoToAvroConvertConfig struct {
	SrcSlot     int                            // source ByteSlot (Proto binary)
	DstSlot     int                            // destination ByteSlot (Avro binary)
	MsgDesc     protoreflect.MessageDescriptor // proto message descriptor
	MsgPool     *grpcutil.ProtoMsgPool         // pooled dynamic messages
	EncProg     *avro.AvroProgram              // encode JSON â†’ Avro
	ScratchPool *sync.Pool                     // *[]byte pool for avro encode scratch
}

// Action converts Proto binary to Avro binary via JSON pivot.
func (cfg *ProtoToAvroConvertConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]

	// Step 1: Proto binary â†’ JSON via pivot buffer
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	// Unmarshal proto binary to dynamic message
	msg := cfg.MsgPool.Get()
	defer cfg.MsgPool.Put(msg)

	if err := proto.Unmarshal(src, msg); err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Marshal dynamic message to JSON
	jsonBytes, err := protojson.Marshal(msg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Step 2: JSON â†’ Avro binary
	buf := cfg.ScratchPool.Get().(*[]byte)
	defer func() {
		*buf = (*buf)[:0]
		cfg.ScratchPool.Put(buf)
	}()

	avroBin, err := avro.AppendJSONToAvro(*buf, jsonBytes, cfg.EncProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy result to arena
	out := ctx.Alloc(len(avroBin))
	copy(out, avroBin)
	ctx.ByteSlots[cfg.DstSlot] = out
	return state.PC + 1
}

// NewProtoToAvroStep creates a proto_to_avro instruction.
func NewProtoToAvroStep(srcSlot, dstSlot int, msgDesc protoreflect.MessageDescriptor,
	encProg *avro.AvroProgram) (engine.Instruction, error) {

	if msgDesc == nil {
		return engine.Instruction{}, fmt.Errorf("proto_to_avro: msgDesc is nil")
	}
	if encProg == nil {
		return engine.Instruction{}, fmt.Errorf("proto_to_avro: encProg is nil")
	}

	cfg := &ProtoToAvroConvertConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		MsgDesc:     msgDesc,
		MsgPool:     grpcutil.NewProtoMsgPool(msgDesc),
		EncProg:     encProg,
		ScratchPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "PROTO_TO_AVRO",
		Action: cfg.Action,
	}, nil
}
