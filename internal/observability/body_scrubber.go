package observability

import (
	"strings"
)

var sensitiveFieldNames = map[string]struct{}{
	"api_key":         {},
	"apikey":          {},
	"secret":          {},
	"password":        {},
	"authorization":   {},
	"private_key":     {},
	"signing_key":     {},
	"credential":      {},
	"credentials":     {},
	"access_token":    {},
	"bearer_token":    {},
	"refresh_token":   {},
	"auth_token":      {},
}

// isSensitiveField checks if a field name (case-insensitive) is in the sensitive list.
func isSensitiveField(key string) bool {
	_, ok := sensitiveFieldNames[strings.ToLower(key)]
	return ok
}

// ScrubJSON replaces the values of sensitive fields in JSON with "[REDACTED]".
// It operates on raw bytes with a single forward pass — no unmarshal/remarshal.
// The input slice is NOT modified; a new slice is returned only if redactions
// were made. If no sensitive fields are found, the original slice is returned
// unchanged (zero allocation).
func ScrubJSON(input []byte) []byte {
	// First pass: scan for sensitive fields without allocating
	// If we find any, we'll do a second pass to build the output
	if !hasSensitiveField(input) {
		return input
	}

	// Second pass: build output with redactions
	return scrubJSONWithRedactions(input)
}

// hasSensitiveField does a quick scan to check if any sensitive fields exist.
func hasSensitiveField(input []byte) bool {
	inString := false
	escaped := false
	i := 0

	for i < len(input) {
		b := input[i]

		if inString {
			if escaped {
				escaped = false
				i++
				continue
			}
			if b == '\\' {
				escaped = true
				i++
				continue
			}
			if b == '"' {
				inString = false
			}
			i++
			continue
		}

		// Not in string
		if b == '"' {
			// Start of a potential key
			i++
			keyStart := i
			inKey := true

			for i < len(input) && inKey {
				if input[i] == '\\' {
					i += 2 // Skip escape sequence
					continue
				}
				if input[i] == '"' {
					key := string(input[keyStart : i])
					if isSensitiveField(key) {
						// Check if this is followed by : and then a string value
						j := i + 1
						for j < len(input) && isWhitespace(input[j]) {
							j++
						}
						if j < len(input) && input[j] == ':' {
							j++
							for j < len(input) && isWhitespace(input[j]) {
								j++
							}
							if j < len(input) && input[j] == '"' {
								return true
							}
						}
					}
					inKey = false
				} else {
					i++
				}
			}
			continue
		}

		i++
	}

	return false
}

// scrubJSONWithRedactions builds output with sensitive values replaced.
func scrubJSONWithRedactions(input []byte) []byte {
	output := make([]byte, 0, len(input))
	inString := false
	escaped := false
	i := 0

	for i < len(input) {
		b := input[i]

		if inString {
			if escaped {
				output = append(output, b)
				escaped = false
				i++
				continue
			}
			if b == '\\' {
				output = append(output, b)
				escaped = true
				i++
				continue
			}
			if b == '"' {
				output = append(output, b)
				inString = false
				i++
				continue
			}
			output = append(output, b)
			i++
			continue
		}

		// Not in string
		if b == '"' {
			// Start of a potential key
			output = append(output, b)
			i++
			keyStart := i
			inKey := true

			for i < len(input) && inKey {
				if input[i] == '\\' {
					output = append(output, input[keyStart:i+2]...)
					i += 2
					keyStart = i
					continue
				}
				if input[i] == '"' {
					key := string(input[keyStart : i])
					output = append(output, input[keyStart : i+1]...)
					i++

					if isSensitiveField(key) {
						// Skip whitespace and look for :
						wsStart := i
						for i < len(input) && isWhitespace(input[i]) {
							i++
						}

						if i < len(input) && input[i] == ':' {
							output = append(output, input[wsStart:i+1]...)
							i++

							wsStart = i
							for i < len(input) && isWhitespace(input[i]) {
								i++
							}
							output = append(output, input[wsStart:i]...)

							// Check if the value is a string
							if i < len(input) && input[i] == '"' {
								// Skip the string value and replace with "[REDACTED]"
								i++ // Skip opening quote
								for i < len(input) {
									if input[i] == '\\' {
										i += 2
										continue
									}
									if input[i] == '"' {
										i++ // Skip closing quote
										break
									}
									i++
								}
								output = append(output, []byte("\"[REDACTED]\"")...)
							}
						}
					}
					inKey = false
				} else {
					i++
				}
			}
			continue
		}

		output = append(output, b)
		i++
	}

	return output
}

// isWhitespace checks if a byte is JSON whitespace.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
