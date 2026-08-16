package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	grpcutil "github.com/amitkhosla/rah/internal/grpc"
)

// GrpcHandler exposes CRUD REST endpoints for gRPC FileDescriptorSet management.
//
//	POST   /grpc/descriptors              — upload raw FileDescriptorSet bytes
//	                                        body: raw binary (.pb file)
//	                                        header X-Descriptor-Name: <name> (required)
//	GET    /grpc/descriptors              — list all sets [{name, services, uploaded_at}]
//	GET    /grpc/descriptors/{name}       — detail: {name, services:[{full_name, methods}], uploaded_at}
//	DELETE /grpc/descriptors/{name}       — delete set from store and registry
type GrpcHandler struct {
	dsm      *DataStoreManager
	registry *grpcutil.DescriptorRegistry
}

// grpcDescriptorSummary is the JSON shape for a single descriptor set.
type grpcDescriptorSummary struct {
	Name       string               `json:"name"`
	Services   []grpcutil.ServiceInfo `json:"services"`
	UploadedAt string               `json:"uploaded_at"` // RFC3339
}

// NewGrpcHandler creates a GrpcHandler backed by dsm and registry.
func NewGrpcHandler(dsm *DataStoreManager, registry *grpcutil.DescriptorRegistry) *GrpcHandler {
	return &GrpcHandler{dsm: dsm, registry: registry}
}

// RegisterHandlers mounts the gRPC descriptor endpoints on mux.
func (h *GrpcHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/grpc/descriptors", h.collectionHandler)
	mux.HandleFunc("/grpc/descriptors/", h.itemHandler)
}

// RegisterGrpcRoutes is the top-level wiring function called from main.go.
// It is a no-op if dsm does not have DomainGRPCDescriptors configured.
func RegisterGrpcRoutes(mux *http.ServeMux, registry *grpcutil.DescriptorRegistry, dsm *DataStoreManager) {
	if dsm == nil || !dsm.IsConfigured(config.DomainGRPCDescriptors) {
		return
	}
	h := NewGrpcHandler(dsm, registry)
	h.RegisterHandlers(mux)
}

// BootstrapGrpcDescriptors loads all stored FileDescriptorSets into registry at startup.
func BootstrapGrpcDescriptors(dsm *DataStoreManager, registry *grpcutil.DescriptorRegistry) error {
	if dsm == nil || !dsm.IsConfigured(config.DomainGRPCDescriptors) {
		return nil
	}
	ctx := context.Background()
	keys, err := dsm.ListKeysByDomain(ctx, config.DomainGRPCDescriptors, GlobalTenant, "")
	if err != nil {
		return err
	}
	for _, key := range keys {
		// Skip sidecar meta keys.
		if strings.HasSuffix(key, "/_meta") {
			continue
		}
		data, ok, err := dsm.GetGlobal(ctx, config.DomainGRPCDescriptors, key)
		if err != nil || !ok {
			continue
		}
		_ = registry.Load(key, data) // log but don't fail on bad stored data
	}
	return nil
}

// â"€â"€â"€ Collection handler â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func (h *GrpcHandler) collectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSets(w, r)
	case http.MethodPost:
		h.uploadDescriptor(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// â"€â"€â"€ Item handler â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func (h *GrpcHandler) itemHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/grpc/descriptors/")
	if name == "" {
		http.Error(w, "descriptor name required in path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getSet(w, r, name)
	case http.MethodDelete:
		h.deleteSet(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// â"€â"€â"€ Handlers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func (h *GrpcHandler) listSets(w http.ResponseWriter, r *http.Request) {
	names := h.registry.ListSets()
	ctx := r.Context()
	result := make([]grpcDescriptorSummary, 0, len(names))
	for _, name := range names {
		summary := grpcDescriptorSummary{
			Name:     name,
			Services: h.registry.ListServices(name),
		}
		if metaRaw, ok, err := h.dsm.GetGlobal(ctx, config.DomainGRPCDescriptors, name+"/_meta"); err == nil && ok {
			summary.UploadedAt = string(metaRaw)
		}
		result = append(result, summary)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (h *GrpcHandler) getSet(w http.ResponseWriter, r *http.Request, name string) {
	services := h.registry.ListServices(name)
	if services == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	summary := grpcDescriptorSummary{
		Name:     name,
		Services: services,
	}
	if metaRaw, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainGRPCDescriptors, name+"/_meta"); err == nil && ok {
		summary.UploadedAt = string(metaRaw)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

func (h *GrpcHandler) uploadDescriptor(w http.ResponseWriter, r *http.Request) {
	name := r.Header.Get("X-Descriptor-Name")
	if name == "" {
		http.Error(w, "X-Descriptor-Name header is required", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.registry.Load(name, data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := h.dsm.PutGlobal(ctx, config.DomainGRPCDescriptors, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	uploadedAt := time.Now().UTC().Format(time.RFC3339)
	if err := h.dsm.PutGlobal(ctx, config.DomainGRPCDescriptors, name+"/_meta", []byte(uploadedAt)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "ok",
		"name":     name,
		"services": h.registry.ListServices(name),
	})
}

func (h *GrpcHandler) deleteSet(w http.ResponseWriter, r *http.Request, name string) {
	h.registry.Delete(name)
	ctx := r.Context()
	if err := h.dsm.DeleteGlobal(ctx, config.DomainGRPCDescriptors, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.dsm.DeleteGlobal(ctx, config.DomainGRPCDescriptors, name+"/_meta") // ignore error
	w.WriteHeader(http.StatusNoContent)
}
