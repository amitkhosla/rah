package emailprovider

// EmailProviderConfig configures a named email provider.
type EmailProviderConfig struct {
	Name        string `json:"name"         yaml:"name"`
	Type        string `json:"type"         yaml:"type"`          // "smtp"
	Host        string `json:"host"         yaml:"host"`
	Port        int    `json:"port"         yaml:"port"`          // default 587
	FromRef     string `json:"from_ref"     yaml:"from_ref"`      // env:VAR or plain address
	PasswordRef string `json:"password_ref" yaml:"password_ref"`  // env:VAR
	TLS         string `json:"tls"          yaml:"tls"`           // "starttls" | "tls" | "none"
}
