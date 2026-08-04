package storage

// StorageProviderConfig configures a named object storage provider.
type StorageProviderConfig struct {
	Name            string `json:"name"             yaml:"name"`
	Type            string `json:"type"             yaml:"type"`            // "s3"
	BucketRef       string `json:"bucket_ref"       yaml:"bucket_ref"`      // env:VAR or plain name
	Region          string `json:"region"           yaml:"region"`
	AccessKeyRef    string `json:"access_key_ref"   yaml:"access_key_ref"`  // env:VAR
	SecretKeyRef    string `json:"secret_key_ref"   yaml:"secret_key_ref"`  // env:VAR
	EndpointURL     string `json:"endpoint_url,omitempty" yaml:"endpoint_url,omitempty"` // for S3-compatible (MinIO, R2)
}
