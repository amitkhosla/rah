package config

// SecretsConfig configures the pluggable credential resolution providers.
// Providers are selected automatically based on the URI prefix of the
// credential value (e.g. "gsm://...", "vault://...", "env:VAR", "file:///path").
//
// Provider bootstrap credentials (e.g. the Vault token, GSM service-account
// path) must themselves use only "env:" or "file://" references — not another
// secrets manager, to avoid circular resolution.
type SecretsConfig struct {
	GSM          GSMConfig                 `json:"gsm,omitempty"          yaml:"gsm,omitempty"`
	Vault        VaultConfig               `json:"vault,omitempty"        yaml:"vault,omitempty"`
	AWSSM        AWSSMConfig               `json:"aws_sm,omitempty"       yaml:"aws_sm,omitempty"`
	Encrypted    EncryptedConfig           `json:"encrypted,omitempty"    yaml:"encrypted,omitempty"`
	OAuth2       OAuth2Config              `json:"oauth2,omitempty"       yaml:"oauth2,omitempty"`
	GoogleToken  GoogleTokenConfig         `json:"google_token,omitempty" yaml:"google_token,omitempty"`
}

// GSMConfig configures the Google Secret Manager provider.
// Authentication uses Application Default Credentials (ADC) by default —
// no explicit credentials are needed when running with GKE Workload Identity
// or a service account attached to the compute instance.
type GSMConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Project is the default GCP project ID when not specified in the gsm:// URI.
	// e.g. "my-gcp-project". Accepts "env:VAR" references.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// CredentialsFile is a path to a service-account JSON key file.
	// Accepts "env:VAR" or "file:///path" references.
	// Leave empty to use ADC (recommended for GKE / Cloud Run).
	CredentialsFile string `json:"credentials_file,omitempty" yaml:"credentials_file,omitempty"`
}

// VaultConfig configures the HashiCorp Vault provider.
type VaultConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Addr is the Vault server URL. Accepts "env:VAULT_ADDR".
	Addr string `json:"addr,omitempty" yaml:"addr,omitempty"`

	// Auth is the authentication method: "token" | "approle" | "kubernetes".
	Auth string `json:"auth,omitempty" yaml:"auth,omitempty"`

	// Role is the Vault role name used for "approle" and "kubernetes" auth.
	Role string `json:"role,omitempty" yaml:"role,omitempty"`

	// Token is the Vault token for "token" auth. Accepts "env:VAULT_TOKEN".
	Token string `json:"token,omitempty" yaml:"token,omitempty"`

	// RoleID and SecretID are used for "approle" auth.
	// Both accept "env:" references.
	RoleID   string `json:"role_id,omitempty"   yaml:"role_id,omitempty"`
	SecretID string `json:"secret_id,omitempty" yaml:"secret_id,omitempty"`
}

// AWSSMConfig configures the AWS Secrets Manager provider.
// Authentication uses the IAM role attached to the EC2 instance or EKS pod
// (via IRSA) by default — no explicit credentials needed.
type AWSSMConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Region is the AWS region. Accepts "env:AWS_REGION".
	Region string `json:"region,omitempty" yaml:"region,omitempty"`

	// AccessKey and SecretKey for explicit credentials.
	// Both accept "env:" references.
	// Leave empty to use the attached IAM role (recommended).
	AccessKey string `json:"access_key,omitempty" yaml:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty" yaml:"secret_key,omitempty"`
}

// OAuth2Config configures the OAuth2 client credentials provider.
// Each named client is registered as a separate entry in Clients.
// URI format: oauth2token://client-name
//
// Example:
//
//	secrets:
//	  oauth2:
//	    clients:
//	      payment-service:
//	        token_url: "https://auth.payment.example.com/token"
//	        client_id: "my-client-id"
//	        client_secret: "env:PAYMENT_CLIENT_SECRET"
//	        scopes: ["payment.read", "payment.write"]
type OAuth2Config struct {
	Clients map[string]OAuth2ClientConfig `json:"clients,omitempty" yaml:"clients,omitempty"`
}

// OAuth2ClientConfig configures a single OAuth2 client credentials flow client.
type OAuth2ClientConfig struct {
	TokenURL     string   `json:"token_url"               yaml:"token_url"`
	ClientID     string   `json:"client_id"               yaml:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty" yaml:"client_secret,omitempty"` // accepts env: refs
	Scopes       []string `json:"scopes,omitempty"        yaml:"scopes,omitempty"`
}

// GoogleTokenConfig configures the Google ID token provider.
// URI format: googletoken://https://your-service.run.app
//
// The provider uses Application Default Credentials (ADC) to fetch a
// Google-signed OIDC token for the given audience. No config is needed when
// running on GKE/Cloud Run — ADC provides the identity automatically.
//
// To enable: go build -tags googletoken ./cmd/rah-gateway/
type GoogleTokenConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// EncryptedConfig configures the AES-256-GCM encrypted-value provider.
// Use this to store secrets encrypted-at-rest inside the config file.
// Values are prefixed with "enc:" followed by base64url(nonce || ciphertext).
//
// Key management: the master key itself must come from "env:" or "file://"
// only — it cannot itself be an "enc:" value.
type EncryptedConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Key is a 32-byte AES-256 master key encoded as standard base64.
	// Accepts "env:RAH_MASTER_KEY" or "file:///run/secrets/master-key".
	Key string `json:"key,omitempty" yaml:"key,omitempty"`

	// Master key for encrypting credential values in the datastore.
	// Priority: MasterKeyEnv > MasterKeyPath > MasterKeyPassphraseEnv+MasterKeySalt > none (noop)
	MasterKeyEnv           string `json:"master_key_env,omitempty"            yaml:"master_key_env,omitempty"`
	MasterKeyPath          string `json:"master_key_path,omitempty"           yaml:"master_key_path,omitempty"`
	MasterKeyPassphraseEnv string `json:"master_key_passphrase_env,omitempty" yaml:"master_key_passphrase_env,omitempty"`
	MasterKeySalt          string `json:"master_key_salt,omitempty"           yaml:"master_key_salt,omitempty"`
	MasterKeyVersion       string `json:"master_key_version,omitempty"        yaml:"master_key_version,omitempty"`
}

// MasterKeyConfig is an intermediate struct used to pass master-key configuration
// to the secrets package without creating a circular import. The secrets package
// converts this to its own MasterKeyConfig type.
type MasterKeyConfig struct {
	KeyEnv        string
	KeyPath       string
	PassphraseEnv string
	Salt          string
	KeyVersion    string
}

// ToMasterKeyConfig converts the EncryptedConfig master-key fields to a
// MasterKeyConfig for use with secrets.LoadMasterKey (after conversion).
func (c EncryptedConfig) ToMasterKeyConfig() MasterKeyConfig {
	return MasterKeyConfig{
		KeyEnv:        c.MasterKeyEnv,
		KeyPath:       c.MasterKeyPath,
		PassphraseEnv: c.MasterKeyPassphraseEnv,
		Salt:          c.MasterKeySalt,
		KeyVersion:    c.MasterKeyVersion,
	}
}
