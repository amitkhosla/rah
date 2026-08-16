package storage

// StorageProviderConfig configures a named object storage provider.
type StorageProviderConfig struct {
	Name   string `json:"name"  yaml:"name"`
	Type   string `json:"type"  yaml:"type"` // "s3" | "gcs" | "local"

	// S3 / S3-compatible fields
	BucketRef    string `json:"bucket_ref,omitempty"    yaml:"bucket_ref,omitempty"`
	Region       string `json:"region,omitempty"        yaml:"region,omitempty"`
	AccessKeyRef string `json:"access_key_ref,omitempty" yaml:"access_key_ref,omitempty"`
	SecretKeyRef string `json:"secret_key_ref,omitempty" yaml:"secret_key_ref,omitempty"`
	EndpointURL  string `json:"endpoint_url,omitempty"  yaml:"endpoint_url,omitempty"`

	// GCS fields
	ProjectID     string `json:"project_id,omitempty"     yaml:"project_id,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty" yaml:"credential_ref,omitempty"` // env:VAR pointing to JSON key file path

	// Local filesystem fields
	RootDir string `json:"root_dir,omitempty" yaml:"root_dir,omitempty"` // base directory for local storage
}
