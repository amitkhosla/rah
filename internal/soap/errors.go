package soap

import "errors"

var (
	ErrNotSOAPEnvelope = errors.New("soap: not a SOAP envelope")
	ErrNoBodyElement   = errors.New("soap: no Body element found")
	ErrMalformed       = errors.New("soap: malformed XML")
)
