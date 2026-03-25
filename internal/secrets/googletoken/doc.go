// Package googletoken implements a secrets.Provider that fetches Google-signed
// OIDC ID tokens for a given audience using Application Default Credentials.
//
// # Use case
//
// When a RAH gateway instance needs to call a downstream service that requires
// caller identity verification (e.g. Cloud Run, Cloud Functions, internal APIs
// protected by Google IAP), it fetches a short-lived OIDC token and attaches it
// as the Authorization Bearer header.
//
// # Build tag
//
// No additional dependencies are needed — this package uses google.golang.org/api
// which is already in go.mod (pulled in by the gsm package). Build with
// -tags googletoken to include the provider:
//
//	go build -tags googletoken ./cmd/rah-gateway/
//
// # URI format
//
//	googletoken://https://service.run.app      — full audience URL
//	googletoken://https://api.internal/service
//
// # Authentication
//
// Uses Application Default Credentials (ADC):
//   - GKE Workload Identity / Cloud Run service account (zero config)
//   - GOOGLE_APPLICATION_CREDENTIALS env var → service account JSON
//   - gcloud auth application-default login (local dev)
//
// # Caching
//
// Tokens are cached until 5 minutes before their expiry (typically 55 minutes
// for a 1-hour token). On cache miss the provider fetches a fresh token.
//
// In gateway.yaml:
//
//	secrets:
//	  google_token:
//	    enabled: true   # required to activate this provider
package googletoken
