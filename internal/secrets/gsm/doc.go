// Package gsm implements a secrets.Provider that resolves credentials from
// Google Secret Manager.
//
// # Build tag
//
// This package requires the GCP Secret Manager SDK. Build with -tags gsm to
// include it:
//
//	go build -tags gsm ./cmd/rah-gateway/
//
// First, add the dependency:
//
//	go get cloud.google.com/go/secretmanager@latest
//
// # Registration
//
// The provider registers itself automatically via init() when the package is
// imported. In your binary's main package, add:
//
//	import _ "github.com/amitkhosla/rah/internal/secrets/gsm"
//
// See cmd/rah-gateway/providers_gsm.go for the pre-wired adapter.
//
// # URI format
//
//	gsm://projects/my-project/secrets/my-secret/versions/latest
//	gsm://projects/my-project/secrets/my-secret/versions/5
//	gsm://projects/my-project/secrets/my-secret   (defaults to /versions/latest)
//	gsm://my-secret                                (requires secrets.gsm.project in config)
//	gsm://my-secret/versions/3                    (requires secrets.gsm.project in config)
//
// # Authentication
//
// Uses Application Default Credentials (ADC) by default:
//   - GKE Workload Identity / Cloud Run service identity (zero config)
//   - GOOGLE_APPLICATION_CREDENTIALS env var â†’ service-account JSON file
//   - gcloud auth application-default login (local development)
//
// For explicit credentials, set secrets.gsm.credentials_file in gateway config:
//
//	secrets:
//	  gsm:
//	    enabled: true
//	    project: "my-gcp-project"
//	    credentials_file: "file:///run/secrets/sa-key.json"
//	    # or: credentials_file: "env:GOOGLE_APPLICATION_CREDENTIALS"
package gsm
