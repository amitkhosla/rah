package steps

import (
	"regexp"
	"strings"
)

// CondKind distinguishes And/Or composite nodes from leaf condition nodes.
type CondKind uint8

const (
	CondAnd  CondKind = 0
	CondOr   CondKind = 1
	CondLeaf CondKind = 2
)

// SourceKind tells the evaluator where to extract the value from.
type SourceKind uint8

const (
	SrcReqBody    SourceKind = 0
	SrcRespBody   SourceKind = 1
	SrcReqHeader  SourceKind = 2
	SrcRespHeader SourceKind = 3
	SrcSlot       SourceKind = 4
)

// CheckKind is the comparison operation applied to the extracted value.
type CheckKind uint8

const (
	CheckExists  CheckKind = 0
	CheckMissing CheckKind = 1
	CheckEq      CheckKind = 2
	CheckNEq     CheckKind = 3
	CheckLt      CheckKind = 4
	CheckGt      CheckKind = 5
	CheckRegex   CheckKind = 6
	CheckIn      CheckKind = 7
)

// DestKind determines what the instruction does when a rule matches.
type DestKind uint8

const (
	DestJump     DestKind = 0 // jump to a named next step
	DestFail     DestKind = 1 // return HTTP error
	DestContinue DestKind = 2 // fall through to next instruction
	DestRetry    DestKind = 3 // trigger retry
	DestDefault  DestKind = 4 // jump to the instruction's DefaultPC
)

// msgPartKind distinguishes literal text from slot references in a message template.
type msgPartKind uint8

const (
	msgPartLiteral msgPartKind = 0
	msgPartSlot    msgPartKind = 1
)

// msgPart is one fragment of a pre-split message template.
// Rendered at runtime by concatenating literals and slot values — zero allocation
// (writes into a reused []byte buffer on the context).
type msgPart struct {
	kind    msgPartKind
	literal string // used when kind == msgPartLiteral
	slotIdx int    // used when kind == msgPartSlot: index into ctx.ByteSlots
}

// ParseMsgTemplate splits a template like "Missing field {slot:0}" into []msgPart.
// Called once at bake time. Zero allocations at runtime.
func ParseMsgTemplate(tpl string) []msgPart {
	var parts []msgPart
	for len(tpl) > 0 {
		start := strings.Index(tpl, "{slot:")
		if start < 0 {
			parts = append(parts, msgPart{kind: msgPartLiteral, literal: tpl})
			break
		}
		if start > 0 {
			parts = append(parts, msgPart{kind: msgPartLiteral, literal: tpl[:start]})
		}
		end := strings.Index(tpl[start:], "}")
		if end < 0 {
			parts = append(parts, msgPart{kind: msgPartLiteral, literal: tpl[start:]})
			break
		}
		inner := tpl[start+6 : start+end] // digits between "{slot:" and "}"
		idx := 0
		for _, c := range inner {
			if c >= '0' && c <= '9' {
				idx = idx*10 + int(c-'0')
			}
		}
		parts = append(parts, msgPart{kind: msgPartSlot, slotIdx: idx})
		tpl = tpl[start+end+1:]
	}
	return parts
}

// CondNode is one node in a flat pre-order condition tree stored in CompiledRule.Nodes.
//
// Layout rules (zero-allocation guarantee):
//   - No dynamic pointer fields except CompiledPattern, which is compiled once at bake time
//     and never changes — GC sees it as a stable root, not a hot-path allocation.
//   - PathKey and ValueStr are indices into CompiledRule.Paths / CompiledRule.Strings,
//     which are bake-time slices — no runtime string copies.
//
// And/Or nodes use ChildStart + ChildCount; leaf nodes use all other fields.
type CondNode struct {
	Kind       CondKind
	SourceKind SourceKind
	Check      CheckKind
	_pad       [1]byte
	PathKey    uint16 // index into CompiledRule.Paths (gjson path / header name / slot index str)
	ValueStr   uint16 // index into CompiledRule.Strings (for Eq/NEq/Regex/In checks)
	ChildStart uint16 // index of first child in []CondNode (And/Or nodes only)
	ChildCount uint8  // number of children (And/Or nodes only)
	_pad2      [3]byte
	ValueFloat float64          // numeric value for Lt/Gt checks
	CompiledPattern *regexp.Regexp // non-nil only when Check == CheckRegex; compiled at bake time
}

// CompiledRule is the baked representation of one validation rule group.
// All string data lives in Paths and Strings slices allocated once at bake time.
type CompiledRule struct {
	Nodes          []CondNode // flat pre-order tree; index 0 is the root
	MsgParts       []msgPart  // pre-split message template; nil unless DestKind == DestFail
	Paths          []string   // deduplicated gjson paths / header names (indexed by CondNode.PathKey)
	Strings        []string   // deduplicated string values for comparisons (indexed by CondNode.ValueStr)
	MatchPC        int        // absolute instruction PC to jump to on match; patched after compilation
	DestKind       DestKind
	Status         int    // HTTP status code for DestFail (e.g. 400, 403)
	TargetStepName string // target flow/step name for DestJump rules; resolved to MatchPC at bake time
}

// CompileCondPattern compiles a regex pattern string at bake time.
// Returns an error if the pattern is invalid.
func CompileCondPattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

// FieldSchema describes one field for validation and future format conversion.
// Stored in the datastore (DomainValidationSchemas) and loaded at bake time.
type FieldSchema struct {
	Name       string
	Path       string // gjson path into the request/response body
	Required   bool
	Type       string   // "string", "number", "bool", "array", "object"
	Pattern    string   // regex string; compiled at bake time into CompiledPattern
	EnumValues []string // valid values for enum-style fields
	MinLen     int      // minimum string/array length (0 = unchecked)
	MaxLen     int      // maximum string/array length (0 = unchecked)

	// Future format conversion fields — populated from OpenAPI/proto/avro imports.
	// Not used by the runtime evaluator; preserved for downstream converters.
	ProtoFieldNum  int32
	ProtoWireType  uint8
	ProtoOptional  bool
	AvroType       string
	AvroSchemaRef  string
}
