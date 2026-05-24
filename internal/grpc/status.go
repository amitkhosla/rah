package grpcutil

import (
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// grpcHTTPStatus maps gRPC status codes to HTTP status codes.
var grpcHTTPStatus = [...]int{
	int(codes.OK):                  200,
	int(codes.Canceled):            499,
	int(codes.Unknown):             500,
	int(codes.InvalidArgument):     400,
	int(codes.DeadlineExceeded):    504,
	int(codes.NotFound):            404,
	int(codes.AlreadyExists):       409,
	int(codes.PermissionDenied):    403,
	int(codes.ResourceExhausted):   429,
	int(codes.FailedPrecondition):  400,
	int(codes.Aborted):             409,
	int(codes.OutOfRange):          400,
	int(codes.Unimplemented):       501,
	int(codes.Internal):            500,
	int(codes.Unavailable):         503,
	int(codes.DataLoss):            500,
	int(codes.Unauthenticated):     401,
}

// GRPCStatusToHTTP maps a gRPC status code to an HTTP status code.
// Unknown or out-of-range codes return 500.
func GRPCStatusToHTTP(code codes.Code) int {
	idx := int(code)
	if idx >= 0 && idx < len(grpcHTTPStatus) {
		if h := grpcHTTPStatus[idx]; h != 0 {
			return h
		}
	}
	return 500
}

// grpcErrorBody is the JSON structure returned for gRPC errors.
type grpcErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details []json.RawMessage `json:"details,omitempty"`
}

// ErrorToJSON serializes a gRPC error into JSON bytes suitable for an HTTP
// response body. Returns nil if err is nil.
//
// Output format:
//
//	{"code":"NOT_FOUND","message":"user not found","details":[...]}
//
// The "details" field is omitted when the status carries no detail messages.
func ErrorToJSON(err error) []byte {
	if err == nil {
		return nil
	}
	s, _ := status.FromError(err)
	body := grpcErrorBody{
		Code:    s.Code().String(),
		Message: s.Message(),
	}

	// Serialize each detail proto to JSON using protojson.
	for _, detail := range s.Proto().GetDetails() {
		raw, merr := protojson.Marshal(detail)
		if merr == nil && len(raw) > 0 {
			body.Details = append(body.Details, json.RawMessage(raw))
		}
	}

	out, _ := json.Marshal(&body)
	return out
}
