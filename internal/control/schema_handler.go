package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// SchemaHandler exposes CRUD REST endpoints for FieldSchema sets.
//
//	GET    /schemas                   — list schema set names
//	GET    /schemas/{name}            — list all fields in a schema set
//	POST   /schemas/{name}            — upsert a field (body: FieldSchema JSON)
//	DELETE /schemas/{name}/{field}    — delete one field
//	DELETE /schemas/{name}            — delete entire schema set
type SchemaHandler struct {
	dsm *DataStoreManager
}

// NewSchemaHandler creates a SchemaHandler backed by dsm.
func NewSchemaHandler(dsm *DataStoreManager) *SchemaHandler {
	return &SchemaHandler{dsm: dsm}
}

// RegisterHandlers mounts the schema endpoints on mux.
func (h *SchemaHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/schemas", h.setsCollectionHandler)
	mux.HandleFunc("/schemas/", h.schemasItemHandler)
}

// setsCollectionHandler handles GET /schemas — returns list of unique schema set names.
func (h *SchemaHandler) setsCollectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainValidationSchemas, GlobalTenant, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seen := make(map[string]struct{}, len(keys))
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		slash := strings.Index(k, "/")
		var setName string
		if slash >= 0 {
			setName = k[:slash]
		} else {
			setName = k
		}
		if _, ok := seen[setName]; !ok {
			seen[setName] = struct{}{}
			names = append(names, setName)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(names)
}

// schemasItemHandler routes GET/POST /schemas/{name} and DELETE /schemas/{name}[/{field}].
func (h *SchemaHandler) schemasItemHandler(w http.ResponseWriter, r *http.Request) {
	// Path is /schemas/{name} or /schemas/{name}/{field}
	rest := strings.TrimPrefix(r.URL.Path, "/schemas/")
	if rest == "" {
		http.Error(w, "schema set name required", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	setName := parts[0]
	fieldName := ""
	if len(parts) == 2 {
		fieldName = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		if fieldName != "" {
			http.Error(w, "GET /schemas/{name} only; no field in path", http.StatusBadRequest)
			return
		}
		h.listFields(w, r, setName)
	case http.MethodPost:
		h.upsertField(w, r, setName)
	case http.MethodDelete:
		if fieldName != "" {
			h.deleteField(w, r, setName, fieldName)
		} else {
			h.deleteSchemaSet(w, r, setName)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *SchemaHandler) listFields(w http.ResponseWriter, r *http.Request, setName string) {
	ctx := r.Context()
	prefix := setName + "/"
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainValidationSchemas, GlobalTenant, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fields := make([]steps.FieldSchema, 0, len(keys))
	for _, k := range keys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainValidationSchemas, k)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			continue
		}
		var fs steps.FieldSchema
		if err := json.Unmarshal(raw, &fs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fields = append(fields, fs)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fields)
}

func (h *SchemaHandler) upsertField(w http.ResponseWriter, r *http.Request, setName string) {
	var fs steps.FieldSchema
	if err := json.NewDecoder(r.Body).Decode(&fs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if fs.Name == "" {
		http.Error(w, `"name" is required`, http.StatusBadRequest)
		return
	}
	raw, err := json.Marshal(fs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	key := setName + "/" + fs.Name
	if err := h.dsm.PutGlobal(ctx, config.DomainValidationSchemas, key, raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": fs.Name})
}

func (h *SchemaHandler) deleteField(w http.ResponseWriter, r *http.Request, setName, fieldName string) {
	ctx := r.Context()
	key := setName + "/" + fieldName
	if err := h.dsm.DeleteGlobal(ctx, config.DomainValidationSchemas, key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SchemaHandler) deleteSchemaSet(w http.ResponseWriter, r *http.Request, setName string) {
	ctx := r.Context()
	prefix := setName + "/"
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainValidationSchemas, GlobalTenant, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, k := range keys {
		if err := h.dsm.DeleteGlobal(ctx, config.DomainValidationSchemas, k); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// RegisterSchemaRoutes mounts the schema CRUD endpoints on mux.
// No-op if the validation_schemas domain is not configured.
func RegisterSchemaRoutes(mux *http.ServeMux, dsm *DataStoreManager) {
	if !dsm.IsConfigured(config.DomainValidationSchemas) {
		return
	}
	h := NewSchemaHandler(dsm)
	h.RegisterHandlers(mux)
}
