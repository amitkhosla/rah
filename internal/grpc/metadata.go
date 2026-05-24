package grpcutil

import (
	"net/http"
	"strings"

	"google.golang.org/grpc/metadata"
)

// MetadataSlotBinding maps a gRPC metadata key to a ByteSlots index.
// Used to capture specific response metadata values into the request context.
type MetadataSlotBinding struct {
	Key  string // gRPC metadata key (lowercase)
	Slot int    // index into ctx.ByteSlots
}

// hopByHopHeaders lists HTTP headers that must never be forwarded as gRPC metadata.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"host":                {},
	"content-length":      {},
}

// HeadersToMetadata converts HTTP request headers into gRPC outgoing metadata.
//
// Headers listed in block (map key = lowercase header name) are skipped.
// Standard hop-by-hop headers are always skipped regardless of block.
// The returned metadata.MD is ready to attach via metadata.NewOutgoingContext.
func HeadersToMetadata(h http.Header, block map[string]struct{}) metadata.MD {
	md := metadata.MD{}
	for name, values := range h {
		lower := strings.ToLower(name)
		if _, hop := hopByHopHeaders[lower]; hop {
			continue
		}
		if block != nil {
			if _, blocked := block[lower]; blocked {
				continue
			}
		}
		for _, v := range values {
			md[lower] = append(md[lower], v)
		}
	}
	return md
}

// MetadataToSlots reads gRPC metadata values into ByteSlots according to bindings.
//
// For each binding, the first value for binding.Key is written to slots[binding.Slot].
// If Slot is out of range or the key has no values, the binding is silently skipped.
func MetadataToSlots(md metadata.MD, bindings []MetadataSlotBinding, slots [][]byte) {
	for _, b := range bindings {
		if b.Slot < 0 || b.Slot >= len(slots) {
			continue
		}
		vals := md.Get(b.Key)
		if len(vals) == 0 {
			continue
		}
		slots[b.Slot] = []byte(vals[0])
	}
}
