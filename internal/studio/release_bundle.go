package studio

import (
	"encoding/json"
	"net/http"

	"gopkg.in/yaml.v3"
)

// releaseBundleHandler serves GET /api/releases/{id}/bundle?format=yaml|json
// It is dispatched from releaseByIDHandler in server.go when the sub-path is "bundle".
//
//nolint:unused
func (s *Server) releaseBundleHandler(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rec, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	switch format {
	case "json":
		// Pretty-print: unmarshal to any then re-marshal with indentation.
		var v any
		if err := json.Unmarshal(rec.Payload, &v); err != nil {
			http.Error(w, "failed to parse payload", http.StatusInternalServerError)
			return
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			http.Error(w, "failed to serialize JSON", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.json"`)
		_, _ = w.Write(out)

	case "yaml":
		// IMPORTANT: unmarshal rec.Payload (json.RawMessage) to any FIRST,
		// then marshal to YAML. Direct yaml.Marshal of json.RawMessage produces
		// base64-encoded bytes, not human-readable YAML.
		var v any
		if err := json.Unmarshal(rec.Payload, &v); err != nil {
			http.Error(w, "failed to parse payload", http.StatusInternalServerError)
			return
		}
		out, err := yaml.Marshal(v)
		if err != nil {
			http.Error(w, "failed to serialize YAML", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.yaml"`)
		_, _ = w.Write(out)

	default:
		http.Error(w, `unsupported format: use "json" or "yaml"`, http.StatusBadRequest)
	}
}
