package secrets

import (
	"context"
	"fmt"

	"rah/internal/config"
)

// gsmProvider resolves "gsm://projects/PROJECT/secrets/NAME/versions/VERSION"
// references via the Google Secret Manager API.
//
// # URI format
//
//	gsm://projects/my-project/secrets/my-secret/versions/latest
//	gsm://projects/my-project/secrets/my-secret/versions/5
//
// When cfg.Project is set, the project can be omitted from the URI:
//
//	gsm://my-secret/versions/latest   (cfg.Project = "my-project")
//
// # Authentication
//
// Uses Application Default Credentials (ADC) by default:
//   - GKE Workload Identity / Cloud Run service identity (zero config)
//   - GOOGLE_APPLICATION_CREDENTIALS env var pointing to a service-account JSON
//   - cfg.CredentialsFile (resolved via env/file bootstrap providers)
//
// # Enabling this provider
//
// Add the SDK dependency:
//
//	go get cloud.google.com/go/secretmanager/apiv1
//	go get google.golang.org/genproto/googleapis/cloud/secretmanager/v1
//
// Then replace the stub body in gsmProvider.Resolve() with the real API call.
type gsmProvider struct {
	project string // default project (optional)
}

func newGSMProvider(_ context.Context, cfg config.GSMConfig, _ Resolver) (*gsmProvider, error) {
	// TODO: initialise the secretmanager.Client here once the SDK dep is added.
	// If cfg.CredentialsFile is set, resolve it via bootstrap and use
	// option.WithCredentialsJSON(keyJSON) when constructing the client.
	return &gsmProvider{project: cfg.Project}, nil
}

func (p *gsmProvider) Scheme() string { return "gsm" }

func (p *gsmProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	// TODO: implement using the Google Secret Manager client library.
	//
	//   import secretmanager "cloud.google.com/go/secretmanager/apiv1"
	//   import smpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	//
	//   resourceName := gsmResourceName(ref, p.project)
	//   result, err := p.client.AccessSecretVersion(ctx,
	//       &smpb.AccessSecretVersionRequest{Name: resourceName})
	//   return result.Payload.Data, err
	//
	return nil, fmt.Errorf("secrets/gsm: provider not yet implemented — add cloud.google.com/go/secretmanager dep and implement Resolve() in gsm_provider.go (ref: %q)", ref)
}
