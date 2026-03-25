// Package awssm implements a secrets.Provider that resolves credentials from
// AWS Secrets Manager.
//
// # Build tag
//
// This package requires the AWS SDK v2. Build with -tags awssm:
//
//	go get github.com/aws/aws-sdk-go-v2/config@latest
//	go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@latest
//	go build -tags awssm ./cmd/rah-gateway/
//
// # Registration
//
// The provider registers itself automatically via init() when the package is
// imported. The pre-wired adapter is in cmd/rah-gateway/providers_awssm.go.
//
// # URI format
//
//	awssm://secret-name                  — uses default region from config / env
//	awssm://region/secret-name           — overrides region for this secret
//	awssm://secret-name#field            — parse JSON response, return field
//	awssm://region/secret-name#field     — region + field
//
// Examples:
//
//	awssm://us-east-1/prod/stripe-key
//	awssm://prod/database#password
//
// # Authentication
//
// Uses the standard AWS credential chain (instance role / IRSA / env vars)
// by default — no explicit credentials needed on EC2 or EKS.
//
// For explicit credentials, set secrets.aws_sm.access_key and .secret_key
// in gateway.yaml (both accept env: references).
//
//	secrets:
//	  aws_sm:
//	    enabled: true
//	    region: "us-east-1"
//	    # access_key: "env:AWS_ACCESS_KEY_ID"     # optional; use IAM role instead
//	    # secret_key: "env:AWS_SECRET_ACCESS_KEY"
package awssm
