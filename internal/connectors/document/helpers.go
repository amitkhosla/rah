package document

import "regexp"

// collectionNameRegex validates collection names to prevent SQL injection.
// Only alphanumeric characters and underscores are allowed.
var collectionNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// sortFieldRegex validates sort field names
var sortFieldRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
