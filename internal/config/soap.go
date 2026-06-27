package config

// SOAPVersion identifies the SOAP protocol version to use.
type SOAPVersion uint8

const (
	SOAPVersion11 SOAPVersion = 1
	SOAPVersion12 SOAPVersion = 2
)

// String returns the SOAP namespace URI for this version.
func (v SOAPVersion) String() string {
	switch v {
	case SOAPVersion12:
		return "http://www.w3.org/2003/05/soap-envelope"
	default:
		return "http://schemas.xmlsoap.org/soap/envelope/"
	}
}

// SOAPConfig holds SOAP protocol configuration.
type SOAPConfig struct {
	// DefaultVersion sets the SOAP version used when building envelopes.
	// Defaults to SOAPVersion11.
	DefaultVersion SOAPVersion `json:"default_version,omitempty" yaml:"default_version,omitempty"`
}
