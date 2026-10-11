package datacache

// FieldKind defines the type of a field in the schema.
type FieldKind uint8

const (
	FieldFloat64 FieldKind = iota // stride=8
	FieldInt64                      // stride=8
	FieldStringN                    // stride=Width (fixed-width bytes)
	FieldEnum                       // stride=1 (uint8)
)

// FieldDef describes a field in the schema.
type FieldDef struct {
	Name  string    // field name
	Kind  FieldKind // field type
	Width int       // FieldFloat64/Int64: 8 (set automatically); FieldStringN: caller-specified; FieldEnum: 1 (set automatically)
}

// ColRegion describes the location and stride of a column within the arena buffer.
type ColRegion struct {
	Offset int // byte offset of this column's start within arena buf
	Stride int // bytes per element in this column
}

// Schema describes the columnar layout of records in an Arena.
type Schema struct {
	Fields     []FieldDef // field definitions
	ColRegions []ColRegion // column metadata; len == len(Fields), computed by BakeSchema
	SortByIdx  int         // field index for sort (-1 = none)
	BufSize    int         // total bytes needed for capacity records (computed)
}

// BakeSchema computes the columnar layout for the given fields and capacity.
// All columns are laid out sequentially in memory:
//   - col[0] at offset 0, stride = fields[0].Width, occupies fields[0].Width * capacity bytes
//   - col[1] at offset (col[0].Offset + col[0].Width * capacity), etc.
func BakeSchema(fields []FieldDef, capacity int) Schema {
	// Ensure Width is set for each field
	for i := range fields {
		switch fields[i].Kind {
		case FieldFloat64:
			fields[i].Width = 8
		case FieldInt64:
			fields[i].Width = 8
		case FieldEnum:
			fields[i].Width = 1
		case FieldStringN:
			// Width must already be set by caller for FieldStringN
			if fields[i].Width == 0 {
				fields[i].Width = 0 // let caller specify
			}
		}
	}

	colRegions := make([]ColRegion, len(fields))
	offset := 0

	// Compute column offsets and strides
	for i := range fields {
		colRegions[i].Stride = fields[i].Width
		colRegions[i].Offset = offset
		offset += fields[i].Width * capacity
	}

	return Schema{
		Fields:     fields,
		ColRegions: colRegions,
		SortByIdx:  -1,
		BufSize:    offset,
	}
}
