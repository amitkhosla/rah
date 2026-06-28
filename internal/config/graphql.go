package config

// GraphQLConfig controls the GraphQL subsystem.
type GraphQLConfig struct {
	SchemaValidation bool `json:"schema_validation,omitempty" yaml:"schema_validation,omitempty"`
}
