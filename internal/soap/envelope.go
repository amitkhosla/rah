package soap

import (
	"github.com/amitkhosla/rah/internal/xml"
)

// Pre-baked SOAP 1.1 namespace and tag bytes, allocated once at package init.
// All Append* functions reference these via sub-slice (zero copy at runtime).
var (
	// SOAP 1.1 namespace and envelope opening tag
	soap11EnvOpenBytes []byte
	soap11EnvCloseBytes []byte

	// SOAP 1.2 namespace and envelope opening tag
	soap12EnvOpenBytes []byte
	soap12EnvCloseBytes []byte

	// Common body tags
	soapBodyOpenBytes []byte
	soapBodyCloseBytes []byte
)

func init() {
	// SOAP 1.1
	soap11EnvOpenBytes = []byte(`<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body>`)
	soap11EnvCloseBytes = []byte(`</soapenv:Body></soapenv:Envelope>`)

	// SOAP 1.2
	soap12EnvOpenBytes = []byte(`<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://www.w3.org/2003/05/soap-envelope"><soapenv:Body>`)
	soap12EnvCloseBytes = []byte(`</soapenv:Body></soapenv:Envelope>`)
}

// AppendSOAP11Envelope wraps bodyXML with a SOAP 1.1 envelope, appending into dst.
// Returns the result with SOAP wrapper applied. Zero allocation at runtime
// (all tag bytes are from pre-baked slabs).
func AppendSOAP11Envelope(dst, bodyXML []byte) []byte {
	dst = append(dst, soap11EnvOpenBytes...)
	dst = append(dst, bodyXML...)
	dst = append(dst, soap11EnvCloseBytes...)
	return dst
}

// AppendSOAP12Envelope wraps bodyXML with a SOAP 1.2 envelope, appending into dst.
// Returns the result with SOAP 1.2 wrapper applied. Zero allocation at runtime.
func AppendSOAP12Envelope(dst, bodyXML []byte) []byte {
	dst = append(dst, soap12EnvOpenBytes...)
	dst = append(dst, bodyXML...)
	dst = append(dst, soap12EnvCloseBytes...)
	return dst
}

// AppendUnwrapBody extracts the Body element content from a SOAP envelope.
// Borrows an XMLScanner from the pool, defers PutXMLScanner (clears src pointer).
// Returns (dst with body content appended, error if envelope is invalid or not SOAP).
func AppendUnwrapBody(dst, envelopeXML []byte) ([]byte, error) {
	scanner := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner) // clears src pointer

	// Validate and skip XML prolog/DTD
	if err := scanner.ValidateAndSkipProlog(); err != nil {
		return dst, err
	}

	// Find Envelope element (namespace-agnostic via FindElement)
	if !scanner.FindElement([]byte("Envelope")) {
		return dst, ErrNotSOAPEnvelope
	}

	// Find Body element inside Envelope
	if !scanner.FindElement([]byte("Body")) {
		return dst, ErrNoBodyElement
	}

	// Extract body content (everything between <Body> and </Body>)
	content, ok := scanner.AppendContent(dst)
	if !ok {
		return dst, ErrMalformed
	}
	return content, nil
}

// AppendParseFault11 extracts faultcode and faultstring from a SOAP 1.1 fault response.
// Assumes envelopeXML contains a <Fault> element with <faultcode> and <faultstring> children.
// Returns (codeDst with code appended, msgDst with string appended, true if fault found; error if malformed).
// Returns (codeDst, msgDst, false) if no fault is found.
func AppendParseFault11(codeDst, msgDst, envelopeXML []byte) ([]byte, []byte, error) {
	scanner := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner)

	if err := scanner.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}

	// Find Envelope
	if !scanner.FindElement([]byte("Envelope")) {
		return codeDst, msgDst, ErrNotSOAPEnvelope
	}

	// Find Body
	if !scanner.FindElement([]byte("Body")) {
		return codeDst, msgDst, ErrNoBodyElement
	}

	// Find Fault inside Body
	if !scanner.FindElement([]byte("Fault")) {
		// No fault; not an error condition
		return codeDst, msgDst, nil
	}

	// Find faultcode
	scanner2 := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner2)
	if err := scanner2.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}
	if scanner2.FindElement([]byte("Envelope")) &&
		scanner2.FindElement([]byte("Body")) &&
		scanner2.FindElement([]byte("Fault")) &&
		scanner2.FindElement([]byte("faultcode")) {
		var ok bool
		codeDst, ok = scanner2.AppendContent(codeDst)
		if !ok {
			codeDst = append(codeDst, []byte("Unknown")...)
		}
	}

	// Find faultstring
	scanner3 := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner3)
	if err := scanner3.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}
	if scanner3.FindElement([]byte("Envelope")) &&
		scanner3.FindElement([]byte("Body")) &&
		scanner3.FindElement([]byte("Fault")) &&
		scanner3.FindElement([]byte("faultstring")) {
		var ok bool
		msgDst, ok = scanner3.AppendContent(msgDst)
		if !ok {
			msgDst = append(msgDst, []byte("Unknown fault")...)
		}
	}

	return codeDst, msgDst, nil
}

// AppendParseFault12 extracts Code/Value and Reason/Text from a SOAP 1.2 fault response.
// Assumes envelopeXML contains a <Fault> element with <Code><Value> and <Reason><Text> structure.
// Returns (codeDst with code appended, msgDst with reason text appended, error if malformed).
// Returns (codeDst, msgDst, nil) if no fault is found.
func AppendParseFault12(codeDst, msgDst, envelopeXML []byte) ([]byte, []byte, error) {
	scanner := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner)

	if err := scanner.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}

	// Find Envelope
	if !scanner.FindElement([]byte("Envelope")) {
		return codeDst, msgDst, ErrNotSOAPEnvelope
	}

	// Find Body
	if !scanner.FindElement([]byte("Body")) {
		return codeDst, msgDst, ErrNoBodyElement
	}

	// Find Fault inside Body
	if !scanner.FindElement([]byte("Fault")) {
		// No fault; not an error condition
		return codeDst, msgDst, nil
	}

	// Find Code/Value
	scanner2 := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner2)
	if err := scanner2.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}
	if scanner2.FindElement([]byte("Envelope")) &&
		scanner2.FindElement([]byte("Body")) &&
		scanner2.FindElement([]byte("Fault")) &&
		scanner2.FindElement([]byte("Code")) &&
		scanner2.FindElement([]byte("Value")) {
		var ok bool
		codeDst, ok = scanner2.AppendContent(codeDst)
		if !ok {
			codeDst = append(codeDst, []byte("Unknown")...)
		}
	}

	// Find Reason/Text
	scanner3 := xml.GetXMLScanner(envelopeXML)
	defer xml.PutXMLScanner(scanner3)
	if err := scanner3.ValidateAndSkipProlog(); err != nil {
		return codeDst, msgDst, err
	}
	if scanner3.FindElement([]byte("Envelope")) &&
		scanner3.FindElement([]byte("Body")) &&
		scanner3.FindElement([]byte("Fault")) &&
		scanner3.FindElement([]byte("Reason")) &&
		scanner3.FindElement([]byte("Text")) {
		var ok bool
		msgDst, ok = scanner3.AppendContent(msgDst)
		if !ok {
			msgDst = append(msgDst, []byte("Unknown reason")...)
		}
	}

	return codeDst, msgDst, nil
}
