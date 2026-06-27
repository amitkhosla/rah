package avro

import "errors"

var (
	ErrRecursiveSchema = errors.New("avro: recursive schema not supported")
	ErrBadWireData     = errors.New("avro: malformed binary data")
)
