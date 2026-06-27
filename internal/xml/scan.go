package xml

import (
	"bytes"
	"errors"
	"sync"
	"unsafe"
)

var (
	ErrDTDRejected   = errors.New("xml: DTD and external entities are rejected")
	ErrMalformed     = errors.New("xml: malformed XML")
	ErrMultipleRoots = errors.New("xml: multiple root elements not supported")
)

// XMLScanner scans a []byte XML document using only byte operations.
// No encoding/xml, no interface{}, no allocations after pool Get.
// Reset via Reset(src) before each use. Return via PutXMLScanner.
type XMLScanner struct {
	src []byte
	pos int
}

var xmlScanPool = sync.Pool{New: func() any { return new(XMLScanner) }}

// GetXMLScanner retrieves a scanner from the pool and resets it to src.
func GetXMLScanner(src []byte) *XMLScanner {
	s := xmlScanPool.Get().(*XMLScanner)
	s.Reset(src)
	return s
}

// PutXMLScanner clears the scanner's src pointer (MUST — src refs request data)
// and returns it to the pool.
func PutXMLScanner(s *XMLScanner) {
	s.src = nil // release reference to per-request data — prevents GC retention
	s.pos = 0
	xmlScanPool.Put(s)
}

// Reset resets the scanner to a new source document.
func (s *XMLScanner) Reset(src []byte) {
	s.src = src
	s.pos = 0
}

// ValidateAndSkipProlog advances past the XML declaration and checks for DTD.
// Returns ErrDTDRejected if a DOCTYPE declaration is found.
// Call once before any Find operations.
func (s *XMLScanner) ValidateAndSkipProlog() error {
	// Check first 512 bytes for DTD
	check := s.src
	if len(check) > 512 {
		check = check[:512]
	}
	if bytes.Contains(check, []byte("<!DOCTYPE")) || bytes.Contains(check, []byte("<!ENTITY")) {
		return ErrDTDRejected
	}
	// Skip XML declaration <?xml ... ?>
	if bytes.HasPrefix(s.src[s.pos:], []byte("<?xml")) {
		end := bytes.Index(s.src[s.pos:], []byte("?>"))
		if end < 0 {
			return ErrMalformed
		}
		s.pos += end + 2
	}
	return nil
}

// FindElement advances to the next open tag whose local name matches name
// (case-insensitive), descending into child elements.
// Returns true if found; s.pos is at the byte after '>'.
func (s *XMLScanner) FindElement(name []byte) bool {
	for {
		if !s.findNextOpenTag() {
			return false
		}
		tagName := s.currentTagName()
		if bytes.EqualFold(tagName, name) {
			return true
		}
		// Non-matching element: continue (don't skip — descend into children)
	}
}

// FindElementN advances to the nth (0-based) open tag matching name.
// Returns true if found.
func (s *XMLScanner) FindElementN(name []byte, n int) bool {
	count := 0
	for {
		if !s.findNextOpenTag() {
			return false
		}
		tagName := s.currentTagName()
		if bytes.EqualFold(tagName, name) {
			if count == n {
				return true
			}
			count++
			// Skip past this match to find subsequent occurrences
			s.skipCurrentElement()
		}
		// Non-matching: descend into children (no skip)
	}
}

// AppendContent reads bytes between the current element's open tag (already
// consumed by FindElement) and its matching close tag, appending into dst.
// Returns (dst, true) on success. s.pos is left after the close tag.
func (s *XMLScanner) AppendContent(dst []byte) ([]byte, bool) {
	// Current pos is right after the open tag's '>'
	// Find matching close tag, handling nesting
	depth := 1
	start := s.pos
	for s.pos < len(s.src) {
		next := bytes.IndexByte(s.src[s.pos:], '<')
		if next < 0 {
			return dst, false
		}
		s.pos += next
		if s.pos+1 >= len(s.src) {
			return dst, false
		}
		if s.src[s.pos+1] == '/' {
			depth--
			if depth == 0 {
				content := s.src[start:s.pos]
				// Advance past close tag
				end := bytes.IndexByte(s.src[s.pos:], '>')
				if end < 0 {
					return dst, false
				}
				s.pos += end + 1
				return append(dst, bytes.TrimSpace(content)...), true
			}
			s.pos++
		} else if s.src[s.pos+1] == '!' || s.src[s.pos+1] == '?' {
			// comment or PI — skip
			s.pos++
		} else {
			depth++
			s.pos++
		}
	}
	return dst, false
}

// AppendAttr finds attribute `name` on the tag that was most recently opened
// (i.e., the tag text between the last '<' and the '>' that FindElement consumed).
// Appends the attribute value into dst.
func (s *XMLScanner) AppendAttr(dst, name []byte) ([]byte, bool) {
	// Scan backwards to find the open tag text
	// The open tag ends at s.pos-1 (the '>').
	// Find the preceding '<'
	tagEnd := s.pos - 1
	tagStart := bytes.LastIndexByte(s.src[:tagEnd], '<')
	if tagStart < 0 {
		return dst, false
	}
	tagBytes := s.src[tagStart+1 : tagEnd]
	return appendAttrValue(dst, tagBytes, name)
}

// Skip advances past the current element including all its children.
func (s *XMLScanner) Skip() {
	s.skipCurrentElement()
}

// --- internal helpers ---

func (s *XMLScanner) findNextOpenTag() bool {
	for s.pos < len(s.src) {
		idx := bytes.IndexByte(s.src[s.pos:], '<')
		if idx < 0 {
			return false
		}
		s.pos += idx
		if s.pos+1 >= len(s.src) {
			return false
		}
		next := s.src[s.pos+1]
		if next == '/' || next == '!' || next == '?' {
			// close tag, comment, PI — skip to '>'
			end := bytes.IndexByte(s.src[s.pos:], '>')
			if end < 0 {
				return false
			}
			s.pos += end + 1
			continue
		}
		// Open tag found; advance past '>'
		end := bytes.IndexByte(s.src[s.pos:], '>')
		if end < 0 {
			return false
		}
		s.pos += end + 1
		return true
	}
	return false
}

// currentTagName returns the local name of the tag whose '>' is at s.pos-1.
// Strips namespace prefix. No allocation — returns sub-slice of s.src.
func (s *XMLScanner) currentTagName() []byte {
	tagEnd := s.pos - 1
	tagStart := bytes.LastIndexByte(s.src[:tagEnd], '<')
	if tagStart < 0 {
		return nil
	}
	raw := s.src[tagStart+1 : tagEnd]
	// Take just the tag name (first token, strip attributes)
	spaceIdx := bytes.IndexAny(raw, " \t\n\r/>")
	if spaceIdx >= 0 {
		raw = raw[:spaceIdx]
	}
	// Strip namespace prefix
	if colon := bytes.IndexByte(raw, ':'); colon >= 0 {
		raw = raw[colon+1:]
	}
	return raw
}

func (s *XMLScanner) skipCurrentElement() {
	// Already consumed the open tag. Skip to matching close.
	s.AppendContent(nil) //nolint — discard content, just advance pos
}

// appendAttrValue extracts the value of attribute `name` from raw tag bytes.
// e.g. tagBytes = `Order type="express" id="1"`, name = "type" → "express"
// Zero allocation: returns sub-slice of tagBytes via append.
func appendAttrValue(dst, tagBytes, name []byte) ([]byte, bool) {
	// Skip the element name (first token)
	i := 0
	for i < len(tagBytes) && tagBytes[i] != ' ' && tagBytes[i] != '\t' && tagBytes[i] != '\n' && tagBytes[i] != '\r' {
		i++
	}
	tagBytes = tagBytes[i:]

	for len(tagBytes) > 0 {
		// Skip whitespace
		i = 0
		for i < len(tagBytes) && (tagBytes[i] == ' ' || tagBytes[i] == '\t' || tagBytes[i] == '\n' || tagBytes[i] == '\r') {
			i++
		}
		tagBytes = tagBytes[i:]
		if len(tagBytes) == 0 {
			break
		}
		// Find '='
		eq := bytes.IndexByte(tagBytes, '=')
		if eq < 0 {
			break
		}
		attrName := bytes.TrimSpace(tagBytes[:eq])
		// Strip namespace from attribute name
		if colon := bytes.IndexByte(attrName, ':'); colon >= 0 {
			attrName = attrName[colon+1:]
		}
		tagBytes = tagBytes[eq+1:]
		if len(tagBytes) == 0 {
			break
		}
		// Parse quoted value
		quote := tagBytes[0]
		if quote != '"' && quote != '\'' {
			break
		}
		tagBytes = tagBytes[1:]
		end := bytes.IndexByte(tagBytes, quote)
		if end < 0 {
			break
		}
		val := tagBytes[:end]
		tagBytes = tagBytes[end+1:]
		if bytes.EqualFold(attrName, name) {
			return append(dst, val...), true
		}
	}
	return dst, false
}

// unsafeString converts []byte to string without allocation.
// Safe because the string is not stored beyond the caller's scope.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}
