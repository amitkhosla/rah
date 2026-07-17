//go:build awssm

package main

// providers_awssm.go registers the AWS Secrets Manager provider when the
// binary is built with -tags awssm.
//
// To enable:
//  1. go get github.com/aws/aws-sdk-go-v2/config@latest
//  2. go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@latest
//  3. go build -tags awssm ./cmd/rah-gateway/
//
// In gateway.yaml:
//
//	secrets:
//	  aws_sm:
//	    enabled: true
//	    region: "us-east-1"     # optional default region; also reads AWS_REGION env var

import (
	"context"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/secrets"
	"github.com/amitkhosla/rah/internal/secrets/awssm"
)

func init() {
	secrets.RegisterProviderFactory("awssm",
		func(ctx context.Context, cfg config.SecretsConfig, bootstrap secrets.Resolver) (secrets.Provider, error) {
			p, err := awssm.New(ctx, cfg.AWSSM, bootstrap)
			if err != nil || p == nil {
				return nil, err
			}
			return p, nil
		},
	)
}
