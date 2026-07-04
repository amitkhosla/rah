package observability

import "testing"

func TestScrubJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no sensitive fields",
			input: `{"model":"gpt-4","temperature":0.7}`,
			want:  `{"model":"gpt-4","temperature":0.7}`,
		},
		{
			name:  "api_key redacted",
			input: `{"api_key":"sk-abc123","model":"gpt-4"}`,
			want:  `{"api_key":"[REDACTED]","model":"gpt-4"}`,
		},
		{
			name:  "apikey redacted",
			input: `{"apikey":"sk-xyz"}`,
			want:  `{"apikey":"[REDACTED]"}`,
		},
		{
			name:  "authorization redacted",
			input: `{"authorization":"Bearer tok","data":"safe"}`,
			want:  `{"authorization":"[REDACTED]","data":"safe"}`,
		},
		{
			name:  "access_token redacted",
			input: `{"access_token":"mytoken","expires_in":3600}`,
			want:  `{"access_token":"[REDACTED]","expires_in":3600}`,
		},
		{
			name:  "nested sensitive field",
			input: `{"config":{"api_key":"secret","url":"https://example.com"}}`,
			want:  `{"config":{"api_key":"[REDACTED]","url":"https://example.com"}}`,
		},
		{
			name:  "token alone not redacted",
			input: `{"input_tokens":100,"output_tokens":50}`,
			want:  `{"input_tokens":100,"output_tokens":50}`,
		},
		{
			name:  "case insensitive",
			input: `{"API_KEY":"sk-abc","Password":"hunter2"}`,
			want:  `{"API_KEY":"[REDACTED]","Password":"[REDACTED]"}`,
		},
		{
			name:  "password redacted",
			input: `{"username":"alice","password":"s3cr3t"}`,
			want:  `{"username":"alice","password":"[REDACTED]"}`,
		},
		{
			name:  "no alloc on clean input returns same slice",
			input: `{"model":"gpt-4"}`,
			want:  `{"model":"gpt-4"}`,
		},
		{
			name:  "escaped quotes in value",
			input: `{"api_key":"val\"ue","data":"test"}`,
			want:  `{"api_key":"[REDACTED]","data":"test"}`,
		},
		{
			name:  "multiple sensitive fields",
			input: `{"api_key":"key1","password":"pass1","username":"user1"}`,
			want:  `{"api_key":"[REDACTED]","password":"[REDACTED]","username":"user1"}`,
		},
		{
			name:  "whitespace around colon",
			input: `{"api_key"  :  "value"}`,
			want:  `{"api_key"  :  "[REDACTED]"}`,
		},
		{
			name:  "empty string value",
			input: `{"api_key":""}`,
			want:  `{"api_key":"[REDACTED]"}`,
		},
		{
			name:  "sensitive key with non-string value (null)",
			input: `{"api_key":null}`,
			want:  `{"api_key":null}`,
		},
		{
			name:  "token_count field not redacted",
			input: `{"token_count":123}`,
			want:  `{"token_count":123}`,
		},
		{
			name:  "private_key redacted",
			input: `{"private_key":"-----BEGIN RSA-----"}`,
			want:  `{"private_key":"[REDACTED]"}`,
		},
		{
			name:  "bearer_token redacted",
			input: `{"bearer_token":"xyz789","public":true}`,
			want:  `{"bearer_token":"[REDACTED]","public":true}`,
		},
		{
			name:  "id field not redacted",
			input: `{"id":"12345","api_key":"secret"}`,
			want:  `{"id":"12345","api_key":"[REDACTED]"}`,
		},
		{
			name:  "key field alone not redacted",
			input: `{"key":"mykey"}`,
			want:  `{"key":"mykey"}`,
		},
		{
			name:  "credentials redacted",
			input: `{"credentials":"aws-creds"}`,
			want:  `{"credentials":"[REDACTED]"}`,
		},
		{
			name:  "refresh_token redacted",
			input: `{"refresh_token":"refresh123","expires_at":"2026-12-31"}`,
			want:  `{"refresh_token":"[REDACTED]","expires_at":"2026-12-31"}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(ScrubJSON([]byte(c.input)))
			if got != c.want {
				t.Errorf("ScrubJSON(%q)\n got: %q\nwant: %q", c.input, got, c.want)
			}
		})
	}
}

// TestScrubJSONNoAlloc verifies that when no sensitive fields are found,
// the original input slice is returned without allocation.
func TestScrubJSONNoAlloc(t *testing.T) {
	input := []byte(`{"model":"gpt-4","temperature":0.7}`)
	output := ScrubJSON(input)
	if &input[0] != &output[0] {
		t.Errorf("ScrubJSON should return the same slice pointer when no redactions are made")
	}
}
