package xml

import (
	"github.com/tidwall/gjson"
)

// AppendXMLToJSON converts XML src to JSON bytes, appending into dst.
// prog defines the element→JSON-key mapping compiled at bake time.
// Uses XMLScanner from pool. Scanner's src pointer cleared before Put.
// Returns error on DTD, malformed XML, or structural issues.
func AppendXMLToJSON(dst, src []byte, prog XMLScanProgram) ([]byte, error) {
	s := GetXMLScanner(src)
	defer PutXMLScanner(s) // clears s.src = nil before pool return

	if err := s.ValidateAndSkipProlog(); err != nil {
		return dst, err
	}

	dst = append(dst, '{')
	first := true

	for i, op := range prog.Ops {
		name := prog.Slab[op.TagOff : op.TagOff+uint16(op.TagLen)]
		key := prog.Slab[op.KeyOff : op.KeyOff+uint16(op.KeyLen)]

		// Resolve actual element index (overflow via ExtTable)
		index := int(op.Index)
		if op.Index == 255 {
			index = int(prog.lookupExt(i).Index)
		}

		// Reset scanner position for each top-level field (re-scan from start)
		// For nested paths this would use a recursive approach, but for flat
		// programs (one level per op) we re-scan from scratch.
		s.Reset(src)
		if err := s.ValidateAndSkipProlog(); err != nil {
			return dst, err
		}

		// For XMLFlagIsAttr: derive attribute name from the JSON key.
		// key is like `"type":` → attr name is "type".
		// This lets a single op find element by TagName and extract a specific attr.
		attrName := deriveAttrName(key)

		var found bool
		if op.Flags&XMLFlagIsAttr != 0 {
			// Attribute: find parent element (by tag name) then extract named attribute.
			if s.FindElement(name) {
				if !first {
					dst = append(dst, ',')
				}
				dst = append(dst, key...)
				dst = append(dst, '"')
				dst, found = s.AppendAttr(dst, attrName)
				if found {
					dst = append(dst, '"')
					first = false
				} else {
					dst = dst[:len(dst)-len(key)-1] // undo key + opening quote
				}
			}
		} else if op.Flags&XMLFlagIsArray != 0 {
			// Collect all matching elements into JSON array
			var hasItems bool
			startLen := len(dst)
			if !first {
				dst = append(dst, ',')
			}
			dst = append(dst, key...)
			dst = append(dst, '[')
			arrFirst := true
			for s.FindElement(name) {
				var content []byte
				content, found = s.AppendContent(nil)
				if found {
					if !arrFirst {
						dst = append(dst, ',')
					}
					dst = append(dst, '"')
					dst = appendJSONEscaped(dst, content)
					dst = append(dst, '"')
					arrFirst = false
					hasItems = true
				}
			}
			dst = append(dst, ']')
			if !hasItems {
				dst = dst[:startLen] // undo if no items found
			} else {
				first = false
			}
		} else {
			// Single element at given index
			if index == 0 {
				found = s.FindElement(name)
			} else {
				found = s.FindElementN(name, index)
			}
			if found {
				var content []byte
				content, found = s.AppendContent(nil)
				if found {
					if !first {
						dst = append(dst, ',')
					}
					dst = append(dst, key...)
					dst = append(dst, '"')
					dst = appendJSONEscaped(dst, content)
					dst = append(dst, '"')
					first = false
				}
			} else if op.Flags&XMLFlagIsOptional == 0 {
				// Required field not found: emit null
				if !first {
					dst = append(dst, ',')
				}
				dst = append(dst, key...)
				dst = append(dst, "null"...)
				first = false
			}
		}
	}

	dst = append(dst, '}')
	return dst, nil
}

// AppendJSONToXML converts JSON src to XML bytes, appending into dst.
// prog defines the JSON-path→XML-tag mapping compiled at bake time.
// Uses gjson for zero-alloc JSON reads. All tag bytes come from prog.Slab.
// No pool borrow needed (gjson operates on raw bytes; slab is pre-allocated).
func AppendJSONToXML(dst, src []byte, prog XMLBuildProgram) ([]byte, error) {
	if len(prog.Ops) == 0 {
		return dst, nil
	}
	dst = appendJSONToXMLOp(dst, src, prog, 0)
	return dst, nil
}

func appendJSONToXMLOp(dst, jsonSrc []byte, prog XMLBuildProgram, opIdx int) []byte {
	op := prog.Ops[opIdx]
	open := prog.Slab[op.OpenOff : op.OpenOff+uint16(op.OpenLen)]
	close_ := prog.Slab[op.CloseOff : op.CloseOff+uint16(op.CloseLen)]

	children := prog.Children[opIdx]
	if len(children) > 0 {
		dst = append(dst, open...)
		for _, childIdx := range children {
			dst = appendJSONToXMLOp(dst, jsonSrc, prog, childIdx)
		}
		dst = append(dst, close_...)
		return dst
	}

	// Leaf: read value from JSON using the tag name as gjson path
	// The tag name is stored in open tag bytes; extract it for gjson path.
	// e.g. open = "<name>" → path = "name"
	path := extractTagName(open)
	result := gjson.GetBytes(jsonSrc, unsafeString(path))
	if !result.Exists() {
		return dst // optional field not present — skip
	}

	dst = append(dst, open...)
	if result.Type == gjson.String {
		dst = append(dst, result.Str...)
	} else {
		dst = append(dst, result.Raw...)
	}
	dst = append(dst, close_...)
	return dst
}

// extractTagName extracts the element name from an open tag byte slice.
// e.g. []byte("<order>") → []byte("order")
// e.g. []byte("<ns:item type=\"x\">") → []byte("item")
// Returns a sub-slice of tag — no allocation.
func extractTagName(tag []byte) []byte {
	if len(tag) < 2 || tag[0] != '<' {
		return tag
	}
	name := tag[1:]
	// Strip trailing '>'
	if len(name) > 0 && name[len(name)-1] == '>' {
		name = name[:len(name)-1]
	}
	// Take first token (before space or '/')
	for i, c := range name {
		if c == ' ' || c == '/' || c == '\t' {
			name = name[:i]
			break
		}
	}
	// Strip namespace prefix
	if colon := indexByte(name, ':'); colon >= 0 {
		name = name[colon+1:]
	}
	return name
}

func indexByte(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}

// appendJSONEscaped appends s to dst with JSON string escaping.
// Handles: `"` → `\"`, `\` → `\\`, control chars → `\uXXXX`.
// Zero allocation: appends directly into dst.
func appendJSONEscaped(dst, s []byte) []byte {
	for _, c := range s {
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hexChar(c>>4), hexChar(c&0xf))
			} else {
				dst = append(dst, c)
			}
		}
	}
	return dst
}

func hexChar(c byte) byte {
	if c < 10 {
		return '0' + c
	}
	return 'a' + c - 10
}

// deriveAttrName strips leading `"` and trailing `":` from a JSON key to get the
// attribute name. E.g. `"type":` → []byte("type").
// Returns a sub-slice of key — no allocation.
func deriveAttrName(key []byte) []byte {
	name := key
	// Strip leading quote
	if len(name) > 0 && name[0] == '"' {
		name = name[1:]
	}
	// Strip trailing `":`
	if len(name) >= 2 && name[len(name)-2] == '"' && name[len(name)-1] == ':' {
		name = name[:len(name)-2]
	}
	return name
}
