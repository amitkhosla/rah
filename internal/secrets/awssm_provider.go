package secrets

import (
	"context"
	"fmt"

	"rah/internal/config"
)

// awssmProvider resolves "awssm://region/secret-name#field" references via
// the AWS Secrets Manager API.
//
// # URI format
//
//	awssm://us-east-1/my-secret            (returns raw string value)
//	awssm://us-east-1/my-secret#password   (parses JSON, returns field)
//
// # Authentication
//
// Uses the AWS SDK credential chain by default (in priority order):
//  1. EC2 instance profile / EKS IRSA — zero config, recommended
//  2. Explicit cfg.AccessKey + cfg.SecretKey (both accept env: references)
//  3. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY environment variables
//
// # Enabling this provider
//
// Add the SDK dependency:
//
//	go get github.com/aws/aws-sdk-go-v2/config
//	go get github.com/aws/aws-sdk-go-v2/service/secretsmanager
//
// Then implement the Resolve() body using the AWS SDK.
type awssmProvider struct {
	region string
}

func newAWSSMProvider(_ context.Context, cfg config.AWSSMConfig, bootstrap Resolver) (*awssmProvider, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	if resolved, err := bootstrap.Resolve(context.Background(), region); err == nil {
		region = string(resolved)
		clear(resolved)
	}
	// TODO: initialise the AWS SM client here using aws/aws-sdk-go-v2.
	return &awssmProvider{region: region}, nil
}

func (p *awssmProvider) Scheme() string { return "awssm" }

func (p *awssmProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	// TODO: implement using the AWS SDK v2.
	//
	//   import "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	//
	//   region, name, field := parseAWSSMRef(ref)
	//   result, err := p.client.GetSecretValue(ctx,
	//       &secretsmanager.GetSecretValueInput{SecretId: &name})
	//   // If field != "", unmarshal JSON and extract the field.
	//   return []byte(*result.SecretString), nil
	//
	return nil, fmt.Errorf("secrets/awssm: provider not yet implemented — add github.com/aws/aws-sdk-go-v2 dep and implement Resolve() in awssm_provider.go (ref: %q)", ref)
}
