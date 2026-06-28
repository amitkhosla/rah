package config

// AvroSchemaSource specifies where to load an Avro schema from.
// At most one of Inline or Subject should be set.
type AvroSchemaSource struct {
	Inline  string `yaml:"inline,omitempty"`  // literal JSON schema string
	Subject string `yaml:"subject,omitempty"` // Confluent registry subject name
	Version string `yaml:"version,omitempty"` // "latest" or numeric version for Subject lookups
}

// AvroConfig holds global Avro registry settings.
// Used by gateway startup to populate avro.SchemaRegistry.
type AvroConfig struct {
	RegistryURL       string `yaml:"registry_url,omitempty"`         // Confluent Schema Registry base URL
	RegistryAPIKeyRef string `yaml:"registry_api_key_ref,omitempty"` // secret reference for API authentication
}
