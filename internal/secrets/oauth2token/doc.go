// Package oauth2token implements a secrets.Provider that fetches OAuth2
// access tokens using the client credentials flow (RFC 6749 §4.4).
//
// # Use case
//
// When RAH needs to call a downstream service that requires a Bearer token
// from an OAuth2 authorization server (e.g. an internal auth service, Auth0,
// Okta, Azure AD), this provider fetches and caches the token.
//
// # Build tag
//
// No additional dependencies are needed — golang.org/x/oauth2 is already in
// go.mod. Build with -tags oauth2token to include the provider:
//
//	go build -tags oauth2token ./cmd/rah-gateway/
//
// # URI format
//
//	oauth2token://client-name
//
// Where client-name matches a key in secrets.oauth2.clients in gateway.yaml.
//
// # Configuration
//
//	secrets:
//	  oauth2:
//	    clients:
//	      payment-service:
//	        token_url:     "https://auth.payment.example.com/token"
//	        client_id:     "my-client-id"
//	        client_secret: "env:PAYMENT_CLIENT_SECRET"
//	        scopes:        ["payment.read"]
//
// In a flow:
//
//	steps:
//	  - action: load_credential
//	    source: payment-token       # name registered via POST /credentials
//	    as: bearer_token
//	  # credential registry maps "payment-token" → "oauth2token://payment-service"
//
// # Caching
//
// Tokens are cached until 1 minute before their expiry (per the expires_in
// field in the token response). If the server returns no expiry, the default
// 30-minute cache TTL applies.
package oauth2token
