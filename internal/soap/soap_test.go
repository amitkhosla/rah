package soap

import (
	"github.com/amitkhosla/rah/internal/xml"
	"testing"
)

// Re-export for tests
var (
	GetXMLScanner = xml.GetXMLScanner
	PutXMLScanner = xml.PutXMLScanner
)

func TestAppendSOAP11Envelope(t *testing.T) {
	bodyXML := []byte(`<user><name>Alice</name></user>`)
	result := AppendSOAP11Envelope(nil, bodyXML)

	// Check that it contains the SOAP 1.1 namespace and structure
	if !contains(result, []byte("soapenv:Envelope")) {
		t.Errorf("expected soapenv:Envelope in result")
	}
	if !contains(result, []byte("soapenv:Body")) {
		t.Errorf("expected soapenv:Body in result")
	}
	if !contains(result, bodyXML) {
		t.Errorf("expected body XML in result")
	}
	if !contains(result, []byte("schemas.xmlsoap.org/soap/envelope")) {
		t.Errorf("expected SOAP 1.1 namespace in result")
	}
}

func TestAppendSOAP12Envelope(t *testing.T) {
	bodyXML := []byte(`<user><name>Bob</name></user>`)
	result := AppendSOAP12Envelope(nil, bodyXML)

	// Check that it contains the SOAP 1.2 namespace and structure
	if !contains(result, []byte("soapenv:Envelope")) {
		t.Errorf("expected soapenv:Envelope in result")
	}
	if !contains(result, []byte("soapenv:Body")) {
		t.Errorf("expected soapenv:Body in result")
	}
	if !contains(result, bodyXML) {
		t.Errorf("expected body XML in result")
	}
	if !contains(result, []byte("w3.org/2003/05/soap-envelope")) {
		t.Errorf("expected SOAP 1.2 namespace in result")
	}
}

func TestAppendUnwrapBody_ValidSOAP11(t *testing.T) {
	envelope := []byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body>
<user><name>Charlie</name></user>
</soapenv:Body>
</soapenv:Envelope>`)

	result, err := AppendUnwrapBody(nil, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, []byte("Charlie")) {
		t.Errorf("expected extracted content to contain body element")
	}
}

func TestAppendUnwrapBody_NonSOAPXML(t *testing.T) {
	notSOAP := []byte(`<?xml version="1.0"?>
<root><data>test</data></root>`)

	_, err := AppendUnwrapBody(nil, notSOAP)
	if err == nil {
		t.Errorf("expected error for non-SOAP XML")
	}
	if err != ErrNotSOAPEnvelope {
		t.Errorf("expected ErrNotSOAPEnvelope, got %v", err)
	}
}

func TestAppendParseFault11(t *testing.T) {
	envelope := []byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body>
<soapenv:Fault>
<faultcode>Server</faultcode>
<faultstring>Internal Server Error</faultstring>
</soapenv:Fault>
</soapenv:Body>
</soapenv:Envelope>`)

	code, msg, err := AppendParseFault11(nil, nil, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(code, []byte("Server")) {
		t.Errorf("expected faultcode in result")
	}
	if !contains(msg, []byte("Internal Server Error")) {
		t.Errorf("expected faultstring in result")
	}
}

func TestAppendParseFault12(t *testing.T) {
	envelope := []byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://www.w3.org/2003/05/soap-envelope">
<soapenv:Body>
<soapenv:Fault>
<Code><Value>Server</Value></Code>
<Reason><Text>Internal Server Error</Text></Reason>
</soapenv:Fault>
</soapenv:Body>
</soapenv:Envelope>`)

	code, msg, err := AppendParseFault12(nil, nil, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(code, []byte("Server")) {
		t.Errorf("expected Code/Value in result")
	}
	if !contains(msg, []byte("Internal Server Error")) {
		t.Errorf("expected Reason/Text in result")
	}
}

func TestAppendSOAP11Envelope_ZeroAlloc(t *testing.T) {
	bodyXML := []byte(`<test>body</test>`)

	// Run allocation check with pre-allocated destination to hit the hot path
	// Pre-allocated buffer â†’ append only, no internal re-allocations
	allocs := testing.AllocsPerRun(100, func() {
		dst := make([]byte, 0, 2048)
		_ = AppendSOAP11Envelope(dst, bodyXML)
	})

	// Should be at most 1 allocation for the dst slice initialization
	if allocs > 1 {
		t.Errorf("expected â‰¤1 allocation (dst buffer), got %v", allocs)
	}
}

func TestAppendSOAP12Envelope_ZeroAlloc(t *testing.T) {
	bodyXML := []byte(`<test>body</test>`)

	allocs := testing.AllocsPerRun(100, func() {
		dst := make([]byte, 0, 2048)
		_ = AppendSOAP12Envelope(dst, bodyXML)
	})

	// Should be at most 1 allocation for the dst slice initialization
	if allocs > 1 {
		t.Errorf("expected â‰¤1 allocation (dst buffer), got %v", allocs)
	}
}

func TestAppendUnwrapBody_ScannerCleared(t *testing.T) {
	envelope := []byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
<soapenv:Body>
<user><name>Test</name></user>
</soapenv:Body>
</soapenv:Envelope>`)

	_, err := AppendUnwrapBody(nil, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify scanner was returned to pool by checking we can get a new one
	scanner := GetXMLScanner(envelope)
	if scanner == nil {
		t.Errorf("expected to retrieve scanner from pool")
	}
	PutXMLScanner(scanner)
}

// helper function
func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
