package steps

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// signBedrockGuardrailRequest applies AWS SigV4 signing to a Bedrock Guardrail HTTP request.
// credentials must be formatted as:
//
//	"ACCESS_KEY_ID:SECRET_ACCESS_KEY"
//	"ACCESS_KEY_ID:SECRET_ACCESS_KEY:SESSION_TOKEN"
//
// region is either a bare region string (e.g. "us-east-1") or a full Bedrock endpoint URL
// (the region will be extracted from the hostname).
func signBedrockGuardrailRequest(req *http.Request, credentials, region string, payload []byte) error {
	if credentials == "" {
		return nil // no credentials — skip signing (e.g. local dev / stub)
	}

	parts := strings.SplitN(credentials, ":", 3)
	if len(parts) < 2 {
		return fmt.Errorf("bedrock guardrail: credentials must be 'ACCESS_KEY_ID:SECRET_ACCESS_KEY'")
	}
	accessKeyID := parts[0]
	secretKey := parts[1]
	sessionToken := ""
	if len(parts) == 3 {
		sessionToken = parts[2]
	}

	// Normalise region: extract bare region from a full URL if needed.
	awsRegion := region
	if strings.HasPrefix(awsRegion, "http") {
		awsRegion = strings.TrimPrefix(awsRegion, "https://")
		awsRegion = strings.TrimPrefix(awsRegion, "http://")
		if slash := strings.Index(awsRegion, "/"); slash >= 0 {
			awsRegion = awsRegion[:slash]
		}
		// hostname: bedrock-runtime.{region}.amazonaws.com
		hostParts := strings.Split(awsRegion, ".")
		if len(hostParts) >= 2 {
			awsRegion = hostParts[1]
		} else {
			awsRegion = "us-east-1"
		}
	}
	if awsRegion == "" {
		awsRegion = "us-east-1"
	}

	now := time.Now().UTC()
	dateTime := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	service := "bedrock"

	req.Header.Set("X-Amz-Date", dateTime)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	req.Host = host

	bodyHash := bedrockSHA256Hex(payload)

	signedHeaderNames := "content-type;host;x-amz-date"
	if sessionToken != "" {
		signedHeaderNames += ";x-amz-security-token"
	}

	canonicalHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + host + "\n" +
		"x-amz-date:" + dateTime + "\n"
	if sessionToken != "" {
		canonicalHeaders += "x-amz-security-token:" + sessionToken + "\n"
	}

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		"", // query string
		canonicalHeaders,
		signedHeaderNames,
		bodyHash,
	}, "\n")

	scope := date + "/" + awsRegion + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + dateTime + "\n" + scope + "\n" +
		bedrockSHA256Hex([]byte(canonicalRequest))

	signingKey := bedrockHMACSHA256(
		bedrockHMACSHA256(
			bedrockHMACSHA256(
				bedrockHMACSHA256([]byte("AWS4"+secretKey), date),
				awsRegion,
			),
			service,
		),
		"aws4_request",
	)

	signature := bedrockHexEncode(bedrockHMACSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID, scope, signedHeaderNames, signature,
	))

	return nil
}

// bedrockHexEncode returns the lowercase hex encoding of b.
// Wraps hex.EncodeToString (already used by llm_adapter_bedrock.go helpers).
func bedrockHexEncode(b []byte) string {
	return bedrockSHA256HexFromBytes(b)
}

// bedrockSHA256HexFromBytes returns the hex encoding of the raw bytes (not a SHA-256 hash).
// Named distinctly to avoid confusion with bedrockSHA256Hex.
func bedrockSHA256HexFromBytes(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexChars[v>>4]
		out[i*2+1] = hexChars[v&0xf]
	}
	return string(out)
}
