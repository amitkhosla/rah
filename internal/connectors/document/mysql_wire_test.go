package document

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
)

// TestMySQLProvider_Interface verifies MySQLProvider implements DocumentProvider
func TestMySQLProvider_Interface(t *testing.T) {
	cfg := config.DocumentConnectorConfig{
		Name: "test",
		Kind: "mysql",
		URI:  "invalid://not-used-in-test",
	}

	provider := &MySQLProvider{
		cfg: cfg,
	}

	// This test just verifies that MySQLProvider has all required methods
	// by checking that they can be called on a nil interface
	var _ DocumentProvider = provider
}

// TestMySQLProvider_ValidateCollectionName tests collection name validation
func TestMySQLProvider_ValidateCollectionName(t *testing.T) {
	cfg := config.DocumentConnectorConfig{
		Name: "test",
		Kind: "mysql",
	}
	provider := &MySQLProvider{cfg: cfg}

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "users", false},
		{"valid with underscore", "user_profiles", false},
		{"valid with numbers", "table123", false},
		{"empty string", "", true},
		{"with dash", "user-profiles", true},
		{"with space", "user profiles", true},
		{"with dot", "user.profiles", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := provider.validateCollectionName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCollectionName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
