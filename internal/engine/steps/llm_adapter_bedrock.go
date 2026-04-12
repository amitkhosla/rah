package steps

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// bedrockAdapter handles AWS Bedrock (Anthropic Claude family).
// BaseURL is either the AWS region (e.g. "us-east-1") or a full Bedrock endpoint URL.
// APIKeyRef = "ACCESS_KEY_ID:SECRET_ACCESS_KEY[:SESSION_TOKEN]"
type bedrockAdapter struct {
	region string
}

func newBedrockAdapter(baseURL string) *bedrockAdapter {
	region := baseURL
	// If baseURL looks like a full URL, extract region from hostname.
	// e.g. "https://bedrock-runtime.us-east-1.amazonaws.com" → "us-east-1"
	if strings.HasPrefix(region, "http") {
		region = strings.TrimPrefix(region, "https://")
		region = strings.TrimPrefix(region, "http://")
		// strip path if any
		if slash := strings.Index(region, "/"); slash >= 0 {
			region = region[:slash]
		}
		// hostname: bedrock-runtime.{region}.amazonaws.com
		parts := strings.Split(region, ".")
		if len(parts) >= 2 {
			region = parts[1] // "us-east-1"
		} else {
			region = "us-east-1"
		}
	}
	if region == "" {
		region = "us-east-1"
	}
	return &bedrockAdapter{region: region}
}

// bedrockAnthropicRequest is the Anthropic wire format for Bedrock:
// identical to anthropicRequest but WITHOUT the top-level "model" field
// (the model is encoded in the URL path).
type bedrockAnthropicRequest struct {
	MaxTokens int                   `json:"max_tokens"`
	System    string                `json:"system,omitempty"`
	Messages  []anthropicReqMessage `json:"messages"`
}

func (b *bedrockAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]anthropicReqMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue // system goes in top-level field
		}
		msgs = append(msgs, anthropicReqMessage{Role: string(m.Role), Content: m.Content})
	}
	system := req.System
	if system == "" {
		for _, m := range req.Messages {
			if m.Role == RoleSystem {
				system = m.Content
				break
			}
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return json.Marshal(bedrockAnthropicRequest{
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
	})
}

func (b *bedrockAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("bedrock: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("bedrock: api error: %s", resp.Error.Message)
	}
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}
	return LLMResponse{
		Content:      text,
		StopReason:   resp.StopReason,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}, nil
}

// Endpoint returns the Bedrock invoke URL for the given model slug.
// baseURL is ignored at call time — the region was captured at construction.
func (b *bedrockAdapter) Endpoint(_, modelSlug string) string {
	return fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		b.region, modelSlug,
	)
}

// AuthHeader returns ("", "") because authentication is performed via SigV4
// signing in SignRequest, not via a simple header value.
func (b *bedrockAdapter) AuthHeader(_ string) (string, string) {
	return "", ""
}

// RequestSigner is an optional extension to ProviderAdapter for providers
// that require request-level signing (e.g. AWS SigV4).
// llm.go checks for this interface after setting the Content-Type header
// and calls SignRequest before executing the HTTP call.
type RequestSigner interface {
	SignRequest(req *http.Request, body []byte, credentials string) error
}

// SignRequest implements RequestSigner by applying AWS SigV4 to the outgoing
// HTTP request. credentials must be formatted as:
//
//	"ACCESS_KEY_ID:SECRET_ACCESS_KEY"
//	"ACCESS_KEY_ID:SECRET_ACCESS_KEY:SESSION_TOKEN"
func (b *bedrockAdapter) SignRequest(req *http.Request, body []byte, credentials string) error {
	parts := strings.SplitN(credentials, ":", 3)
	if len(parts) < 2 {
		return fmt.Errorf("bedrock: credentials must be 'ACCESS_KEY_ID:SECRET_ACCESS_KEY'")
	}
	accessKeyID := parts[0]
	secretKey := parts[1]
	sessionToken := ""
	if len(parts) == 3 {
		sessionToken = parts[2]
	}

	now := time.Now().UTC()
	dateTime := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	service := "bedrock"

	// Set required headers before signing; Content-Type already set by caller.
	req.Header.Set("X-Amz-Date", dateTime)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	// Ensure Host is populated (required by SigV4).
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	req.Host = host

	// Body hash
	bodyHash := bedrockSHA256Hex(body)

	// Signed header set (must be sorted lexicographically).
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
		"", // query string (empty for Bedrock invoke)
		canonicalHeaders,
		signedHeaderNames,
		bodyHash,
	}, "\n")

	// String to sign
	scope := date + "/" + b.region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + dateTime + "\n" + scope + "\n" +
		bedrockSHA256Hex([]byte(canonicalRequest))

	// Derive signing key: HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), service), "aws4_request")
	signingKey := bedrockHMACSHA256(
		bedrockHMACSHA256(
			bedrockHMACSHA256(
				bedrockHMACSHA256([]byte("AWS4"+secretKey), date),
				b.region,
			),
			service,
		),
		"aws4_request",
	)

	signature := hex.EncodeToString(bedrockHMACSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID, scope, signedHeaderNames, signature,
	))

	return nil
}

// bedrockSHA256Hex returns the lowercase hex SHA-256 digest of data.
func bedrockSHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// bedrockHMACSHA256 returns the HMAC-SHA256 of data keyed by key.
func bedrockHMACSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
