package control

// GraphQLStepDescriptors returns the step descriptors for GraphQL-related steps.
func GraphQLStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:           "graphql_call",
			Title:          "GraphQL Call",
			Category:       "http",
			Capability:     "graphql",
			Description:    "Execute a GraphQL query against an upstream endpoint via HTTP POST. The query string is baked at compile time; variables may be supplied dynamically from a slot at runtime.",
			SupportsNested: false,
			Defaults: map[string]string{
				"url":            "",
				"query":          "",
				"timeout_ms":     "10000",
				"fail_on_errors": "false",
			},
			Fields: []StepField{
				sf("url", "Endpoint URL (Static)", "Static URL of the upstream GraphQL endpoint (leave empty to use url_slot for dynamic URLs)", "https://api.example.com/graphql"),
				sf("url_slot", "Endpoint URL (Dynamic Slot)", "Variable name whose slot holds the GraphQL endpoint URL at runtime (use instead of url for dynamic values)", "gql_url"),
				sf("query", "GraphQL Query", "The GraphQL query or mutation string (baked at compile time and JSON-escaped into the request body)", "{ user(id: \"1\") { name email } }"),
				sf("variables_slot", "Variables Slot", "Variable name whose slot holds a JSON object with query variables (optional; omit for queries with no variables)", "gql_vars"),
				sf("data_slot", "Data Output Slot", "Variable name whose slot will store the response 'data' field JSON (optional)", "gql_data"),
				sf("errors_slot", "Errors Output Slot", "Variable name whose slot will store the response 'errors' array JSON (optional)", "gql_errors"),
				sf("timeout_ms", "Timeout (ms)", "Request timeout in milliseconds (default: 10000 = 10 seconds)", "10000"),
				sf("fail_on_errors", "Fail on GraphQL Errors", "Set to 'true' to mark the request as failed (HTTP 400) when the response contains a non-empty errors array", "false"),
			},
		},
		{
			Type:           "graphql_get",
			Title:          "GraphQL Get Field",
			Category:       "format",
			Capability:     "graphql",
			Description:    "Extract a single field from GraphQL response data using a static gjson path. Source slot should hold the 'data' JSON from a previous graphql_call step.",
			SupportsNested: false,
			Defaults: map[string]string{
				"path": "",
			},
			Fields: []StepField{
				sf("path", "gjson Path", "Static gjson path to extract from the data JSON (e.g. 'user.name', 'products.0.id')", "user.name"),
			},
		},
		{
			Type:           "parse_graphql_error",
			Title:          "Parse GraphQL Error",
			Category:       "format",
			Capability:     "graphql",
			Description:    "Extract the first error message string from a GraphQL errors array. Source slot should hold the 'errors' array JSON from a previous graphql_call step.",
			SupportsNested: false,
			Fields:         []StepField{},
		},
	}
}
