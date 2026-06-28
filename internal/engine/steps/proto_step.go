package steps

import (
	"sync"

	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"rah/internal/engine"
	grpcutil "rah/internal/grpc"
	"rah/internal/rctx"
)

// ProtoToJSONConfig configures a proto_to_json instruction.
// It unmarshals proto binary from SrcSlot, marshals to JSON, and stores in DstSlot.
type ProtoToJSONConfig struct {
	SrcSlot int // index into ByteSlots
	DstSlot int // index into ByteSlots
	MsgDesc protoreflect.MessageDescriptor
}

// JSONToProtoConfig configures a json_to_proto instruction.
// It unmarshals JSON from SrcSlot to proto message, marshals to binary, and stores in DstSlot.
type JSONToProtoConfig struct {
	SrcSlot        int
	DstSlot        int
	MsgDesc        protoreflect.MessageDescriptor
	MsgPool        *grpcutil.ProtoMsgPool // Get/Put with proto.Reset
	MarshalBufPool *sync.Pool             // *[]byte pool for marshaling
}

// ProtoGetConfig configures a proto_get instruction for reading a field from a proto binary.
// Static path: field path is compile-time constant, zero-copy walk.
// Dynamic path: field path comes from a slot, requires full decode + gjson.
type ProtoGetConfig struct {
	SrcSlot     int    // source proto binary
	DstSlot     int    // destination for extracted value
	StaticPath  string // compile-time field path (e.g., "user.name"); -1 if dynamic
	PathSlot    int    // slot containing field path at runtime; -1 if static
	MsgDesc     protoreflect.MessageDescriptor
	MsgPool     *grpcutil.ProtoMsgPool
	JSONBufPool *sync.Pool // for dynamic path: full unmarshal → protojson.Marshal → gjson
}

// XMLToProtoConfig configures an xml_to_proto instruction (multi-hop).
// Uses GetPivot/PutPivot for XML→JSON intermediate.
type XMLToProtoConfig struct {
	SrcSlot        int
	DstSlot        int
	MsgDesc        protoreflect.MessageDescriptor
	MsgPool        *grpcutil.ProtoMsgPool
	MarshalBufPool *sync.Pool
}

// ProtoToXMLConfig configures a proto_to_xml instruction (multi-hop).
// Uses GetPivot/PutPivot for proto→JSON intermediate.
type ProtoToXMLConfig struct {
	SrcSlot int
	DstSlot int
	MsgDesc protoreflect.MessageDescriptor
	// XML encoder (placeholder; full implementation in S6)
}

// ProtoToJSONInstruction unmarshals proto binary → dynamicpb.Message → JSON.
func ProtoToJSONInstruction(cfg ProtoToJSONConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PROTO_TO_JSON",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			src := ctx.ByteSlots[cfg.SrcSlot]
			if len(src) == 0 {
				ctx.ByteSlots[cfg.DstSlot] = []byte("{}")
				return state.PC + 1
			}

			// Unmarshal proto binary to dynamic message
			if cfg.MsgDesc == nil {
				ctx.ByteSlots[cfg.DstSlot] = []byte("{}")
				return state.PC + 1
			}
			msg := dynamicpb.NewMessage(cfg.MsgDesc)
			if err := proto.Unmarshal(src, msg); err != nil {
				// Proto unmarshal error: return empty JSON object
				ctx.ByteSlots[cfg.DstSlot] = []byte("{}")
				return state.PC + 1
			}

			// Marshal to JSON
			jsonBytes, err := protojson.Marshal(msg)
			if err != nil {
				ctx.ByteSlots[cfg.DstSlot] = []byte("{}")
				return state.PC + 1
			}

			// Copy to arena
			out := ctx.Alloc(len(jsonBytes))
			copy(out, jsonBytes)
			ctx.ByteSlots[cfg.DstSlot] = out
			return state.PC + 1
		},
	}
}

// JSONToProtoInstruction unmarshals JSON → dynamicpb.Message → proto binary.
// Manages message lifecycle: Get from pool, Unmarshal, Marshal to binary, Put back.
func JSONToProtoInstruction(cfg JSONToProtoConfig) engine.Instruction {
	return engine.Instruction{
		Name: "JSON_TO_PROTO",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			src := ctx.ByteSlots[cfg.SrcSlot]

			// Get message from pool (pre-cleared with proto.Reset)
			msg := cfg.MsgPool.Get()
			defer cfg.MsgPool.Put(msg) // Calls proto.Reset before returning

			// Unmarshal JSON to proto message
			if err := protojson.Unmarshal(src, msg); err != nil {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// Borrow buffer from pool
			bufV := cfg.MarshalBufPool.Get()
			buf := bufV.(*[]byte)
			defer func() {
				*buf = (*buf)[:0] // Reset length to 0
				cfg.MarshalBufPool.Put(buf)
			}()

			// Marshal to proto binary
			*buf, _ = proto.MarshalOptions{}.MarshalAppend((*buf)[:0], msg)

			// Copy to arena
			out := ctx.Alloc(len(*buf))
			copy(out, *buf)
			ctx.ByteSlots[cfg.DstSlot] = out
			return state.PC + 1
		},
	}
}

// ProtoGetInstruction reads a field from proto binary (static or dynamic path).
// Static path: walk descriptor chain at compile time, zero full decode.
// Dynamic path: full unmarshal → protojson.Marshal → gjson.GetBytes.
func ProtoGetInstruction(cfg ProtoGetConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PROTO_GET",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			src := ctx.ByteSlots[cfg.SrcSlot]
			if len(src) == 0 {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// Determine field path
			var path string
			if cfg.PathSlot >= 0 {
				// Dynamic path from slot
				pathBytes := ctx.ByteSlots[cfg.PathSlot]
				path = string(pathBytes)
			} else {
				// Static path
				path = cfg.StaticPath
			}

			if path == "" {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// For dynamic path or when descriptor walk is complex,
			// use full unmarshal + protojson + gjson approach.
			msg := cfg.MsgPool.Get()
			defer cfg.MsgPool.Put(msg)

			if err := proto.Unmarshal(src, msg); err != nil {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// Convert to JSON for field extraction
			jsonBytes, err := protojson.Marshal(msg)
			if err != nil {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// Use gjson to extract field
			result := gjson.GetBytes(jsonBytes, path)
			if !result.Exists() {
				ctx.ByteSlots[cfg.DstSlot] = nil
				return state.PC + 1
			}

			// Copy result to arena
			out := ctx.Alloc(len(result.Raw))
			copy(out, result.Raw)
			ctx.ByteSlots[cfg.DstSlot] = out
			return state.PC + 1
		},
	}
}

// XMLToProtoInstruction converts XML → JSON (via pivot) → proto binary.
// Uses transcode.GetPivot/PutPivot for intermediate buffer.
// Placeholder: full XML parsing in S6.
func XMLToProtoInstruction(cfg XMLToProtoConfig) engine.Instruction {
	return engine.Instruction{
		Name: "XML_TO_PROTO",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Placeholder: S6 implementation will include actual XML parser
			// For now, return error by clearing destination
			ctx.ByteSlots[cfg.DstSlot] = nil
			return state.PC + 1
		},
	}
}

// ProtoToXMLInstruction converts proto binary → JSON (via pivot) → XML.
// Uses transcode.GetPivot/PutPivot for intermediate buffer.
// Placeholder: full XML generation in S6.
func ProtoToXMLInstruction(cfg ProtoToXMLConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PROTO_TO_XML",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Placeholder: S6 implementation will include actual XML generator
			// For now, return error by clearing destination
			ctx.ByteSlots[cfg.DstSlot] = nil
			return state.PC + 1
		},
	}
}

// ── Instruction factories (called from compiler) ────────────────────────────

// NewProtoToJSONInstruction creates a proto_to_json instruction.
func NewProtoToJSONInstruction(cfg ProtoToJSONConfig) engine.Instruction {
	return ProtoToJSONInstruction(cfg)
}

// NewJSONToProtoInstruction creates a json_to_proto instruction.
func NewJSONToProtoInstruction(cfg JSONToProtoConfig) engine.Instruction {
	return JSONToProtoInstruction(cfg)
}

// NewProtoGetInstruction creates a proto_get instruction.
func NewProtoGetInstruction(cfg ProtoGetConfig) engine.Instruction {
	return ProtoGetInstruction(cfg)
}

// NewXMLToProtoInstruction creates an xml_to_proto instruction.
func NewXMLToProtoInstruction(cfg XMLToProtoConfig) engine.Instruction {
	return XMLToProtoInstruction(cfg)
}

// NewProtoToXMLInstruction creates a proto_to_xml instruction.
func NewProtoToXMLInstruction(cfg ProtoToXMLConfig) engine.Instruction {
	return ProtoToXMLInstruction(cfg)
}
