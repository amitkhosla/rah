package studio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/observability"
	rahsync "github.com/amitkhosla/rah/internal/sync"
)

//go:embed ui/dist
var uiFS embed.FS

type ServerConfig struct {
	Targets      []Target `json:"targets"`
	StoreKind    string   `json:"store_kind"`
	StorePath    string   `json:"store_path"`
	ObsStoreType string   `json:"obs_store_type,omitempty"` // "memory", "postgres", "redis" â€” empty = proxy to gateway
	ObsStoreDSN  string   `json:"obs_store_dsn,omitempty"`  // connection string for postgres/redis
	ObsMaxLogs   int      `json:"obs_max_logs,omitempty"`   // max access log entries (default 10000)
	ObsMaxTraces int      `json:"obs_max_traces,omitempty"` // max trace entries (default 500)

	// Auth â€” Studio's own user store (independent of the gateway management API).
	AuthEnabled   bool             `json:"auth_enabled,omitempty"`    // require login; default false
	AuthRealm     string           `json:"auth_realm,omitempty"`      // WWW-Authenticate realm
	AuthUsers     []StudioSeedUser `json:"auth_users,omitempty"`      // config-file seed users (bcrypt hashes)
	AuthStorePath string           `json:"auth_store_path,omitempty"` // path to encrypted user file; empty = memory only
	MCPSecret     string           `json:"mcp_secret,omitempty"`

	// Deployments lists named gateway clusters (topology-agnostic).
	Deployments []Deployment `json:"deployments,omitempty" yaml:"deployments,omitempty"`
	// Environments defines ordered promotion stages with gates and rollout plans.
	Environments []EnvironmentConfig `json:"environments,omitempty" yaml:"environments,omitempty"`
	// SandboxDeployment names the entry in Deployments[] that the sync button
	// always routes to. Empty = legacy behaviour (proxy to default gateway).
	SandboxDeployment string `json:"sandbox_deployment,omitempty" yaml:"sandbox_deployment,omitempty"`

	// Machine token store path. Empty = memory only (tokens lost on restart).
	TokenStorePath string `json:"token_store_path,omitempty" yaml:"token_store_path,omitempty"`

	// OIDC configures one or more OpenID Connect identity providers for SSO.
	OIDC *OIDCConfig `json:"oidc,omitempty" yaml:"oidc,omitempty"`

	// Authz configures the optional external authorization service.
	Authz *AuthzConfig `json:"authz,omitempty" yaml:"authz,omitempty"`

	// SCIM configures SCIM 2.0 provisioning from an enterprise IDP.
	SCIM *SCIMConfig `json:"scim,omitempty" yaml:"scim,omitempty"`
}



type DeployRequest struct {
	ReleaseID             string            `json:"release_id,omitempty"`
	InstructionSetVersion string            `json:"instruction_set_version,omitempty"`
	APIVersions           map[string]string `json:"api_versions,omitempty"`
	Levels                []string          `json:"levels"`
	TargetNames           []string          `json:"target_names"`
	Payload               json.RawMessage   `json:"payload,omitempty"`
}

type DeployResult struct {
	Target    string `json:"target"`
	URL       string `json:"url"`
	Status    int    `json:"status"`
	ReleaseID string `json:"release_id"`
	Error     string `json:"error,omitempty"`
}

type DeployRecord struct {
	At        timeJSON       `json:"at"`
	ReleaseID string         `json:"release_id"`
	Levels    []string       `json:"levels"`
	Targets   []string       `json:"targets"`
	Results   []DeployResult `json:"results"`
}

type LintSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

type ReleaseDeployResult struct {
	Target  string `json:"target"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type EnvDeployment struct {
	DeployedAt      time.Time             `json:"deployed_at"`
	Status          string                `json:"status"`
	ByUser          string                `json:"by_user"`
	Results         []ReleaseDeployResult `json:"results,omitempty"`
	VersionID       string                `json:"version_id,omitempty"`
	CurrentPhase    int                   `json:"current_phase,omitempty"`
	ApprovalStatus  string                `json:"approval_status,omitempty"` // "pending" | "approved" | "expired"
	ApprovalRequired bool                 `json:"approval_required,omitempty"`
	ApprovedBy      string                `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time            `json:"approved_at,omitempty"`
}

type ReleaseRecord struct {
	ReleaseID             string                   `json:"release_id"`
	CreatedAt             timeJSON                 `json:"created_at"`
	InstructionSetVersion string                   `json:"instruction_set_version,omitempty"`
	APIVersions           map[string]string        `json:"api_versions,omitempty"`
	Payload               json.RawMessage          `json:"payload"`
	BundleHash            string                   `json:"bundle_hash,omitempty"`
	GitCommit             string                   `json:"git_commit,omitempty"`
	GitBranch             string                   `json:"git_branch,omitempty"`
	GitRepo               string                   `json:"git_repo,omitempty"`
	SourcePath            string                   `json:"source_path,omitempty"`
	Author                string                   `json:"author,omitempty"`
	Tag                   string                   `json:"tag,omitempty"`
	LintSummary           LintSummary              `json:"lint_summary,omitempty"`
	Environments          map[string]EnvDeployment `json:"environments,omitempty"`
	// Release management extensions.
	Name            string        `json:"name,omitempty"`
	Description     string        `json:"description,omitempty"`
	Labels          []string      `json:"labels,omitempty"`
	IncludeAll      *bool         `json:"include_all,omitempty"` // nil = true (all included by default)
	Include         []ReleaseItem `json:"include,omitempty"`
	Exclude         []ExcludeItem `json:"exclude,omitempty"`
	BaseVersionID   string        `json:"base_version_id,omitempty"`
	FlowBaselineTag string        `json:"flow_baseline_tag,omitempty"`
	Status          string        `json:"status,omitempty"` // "draft" | "published" | "voided"
}

// bundleWrapper is the JSON envelope accepted by POST /api/releases when the
// caller wants to include metadata alongside the bundle in a single JSON body.
type bundleWrapper struct {
	Bundle          *control.UnifiedSyncRequest `json:"bundle"`
	Tag             string                      `json:"tag,omitempty"`
	Name            string                      `json:"name,omitempty"`
	Description     string                      `json:"description,omitempty"`
	Labels          []string                    `json:"labels,omitempty"`
	GitCommit       string                      `json:"git_commit,omitempty"`
	GitBranch       string                      `json:"git_branch,omitempty"`
	GitRepo         string                      `json:"git_repo,omitempty"`
	SourcePath      string                      `json:"source_path,omitempty"`
	Author          string                      `json:"author,omitempty"`
	IncludeAll      *bool                       `json:"include_all,omitempty"`
	Include         []ReleaseItem               `json:"include,omitempty"`
	Exclude         []ExcludeItem               `json:"exclude,omitempty"`
	FlowBaselineTag string                      `json:"flow_baseline_tag,omitempty"`
}

// releaseMeta holds the non-bundle metadata fields for a release.
type releaseMeta struct {
	Tag        string
	GitCommit  string
	GitBranch  string
	GitRepo    string
	SourcePath string
	Author     string
}

// CreateReleaseResponse is returned by POST /api/releases.
type CreateReleaseResponse struct {
	ReleaseID   string              `json:"release_id,omitempty"`
	LintSummary LintSummary         `json:"lint_summary"`
	Warnings    []string            `json:"warnings,omitempty"`
	Issues      []rahsync.LintIssue `json:"issues,omitempty"`
}

// ReleaseListResponse is returned by GET /api/releases.
type ReleaseListResponse struct {
	Releases   []ReleaseRecord `json:"releases"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Total      int             `json:"total"`
}

type Target struct {
	Name  string   `json:"name"`
	Level string   `json:"level"`
	URLs  []string `json:"urls"`
}

type FieldDef struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Placeholder string `json:"placeholder"`
}

type PaletteBlock struct {
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	Category       string            `json:"category"`
	Capability     string            `json:"capability"`
	SupportsNested bool              `json:"supports_nested"`
	NextHints      []string          `json:"next_hints,omitempty"`
	Defaults       map[string]string `json:"defaults"`
	Fields         []FieldDef        `json:"fields,omitempty"`
}

// fld constructs a FieldDef concisely for use in defaultBlocks.
func fld(key, label, desc, ph string) FieldDef {
	return FieldDef{Key: key, Label: label, Description: desc, Placeholder: ph}
}

type SchemaResponse struct {
	Version    string         `json:"version"`
	Categories []string       `json:"categories"`
	Blocks     []PaletteBlock `json:"blocks"`
}

type SuggestionResponse struct {
	StepType        string   `json:"step_type"`
	SuggestedFlows  []string `json:"suggested_flows"`
	SuggestedBlocks []string `json:"suggested_blocks"`
	Notes           []string `json:"notes"`
}

type OpenAPIImportRequest struct {
	Spec string `json:"spec"`
}

// openAPICondConfig mirrors control.CondConfig â€” local copy to avoid circular import.
type openAPICondConfig struct {
	Op       string              `json:"op,omitempty"`
	Source   string              `json:"source,omitempty"`
	Path     string              `json:"path,omitempty"`
	Check    string              `json:"check,omitempty"`
	Value    string              `json:"value,omitempty"`
	ValueNum float64             `json:"valueNum,omitempty"`
	InValues []string            `json:"inValues,omitempty"`
	Children []openAPICondConfig `json:"children,omitempty"`
}

// openAPIOnMatchConfig mirrors control.OnMatchConfig.
type openAPIOnMatchConfig struct {
	Dest    string `json:"dest"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// openAPIRuleConfig mirrors control.RuleConfig.
type openAPIRuleConfig struct {
	Label   string               `json:"label,omitempty"`
	When    openAPICondConfig    `json:"when"`
	OnMatch openAPIOnMatchConfig `json:"onMatch"`
}

type ImportedAPI struct {
	Name               string              `json:"name"`
	Path               string              `json:"path"`
	Method             string              `json:"method"`
	ValidateRouteRules []openAPIRuleConfig `json:"validateRouteRules,omitempty"`
}

type OpenAPIImportResponse struct {
	Source string        `json:"source"`
	APIs   []ImportedAPI `json:"apis"`
}

type timeJSON struct{ T string }

func nowJSON() timeJSON { return timeJSON{T: nowUTC().Format(time.RFC3339)} }

type Server struct {
	config            ServerConfig
	managementBaseURL *url.URL
	httpClient        *http.Client
	targets           []Target
	store             ReleaseStore
	chatStore         ChatHistoryStore
	auditStore        AuditStore

	// Auth — Studio's own user store + session map.
	// Both are nil when auth is disabled (open access mode).
	userStore      *StudioUserStore
	sessions       *SessionStore
	tokenStore     *StudioTokenStore
	oidcStateStore *oidcStateStore
	authzClient    *ExternalAuthzClient

	// gatewayBasicCred is the Authorization header value forwarded to the management
	// API on every proxy call. Read from RAH_GATEWAY_AUTH_USERNAME / RAH_GATEWAY_AUTH_PASSWORD
	// env vars at startup. Empty means no auth header is added (gateway has no auth).
	gatewayBasicCred string

	historyMu sync.RWMutex
	history   []DeployRecord
	seqMu     sync.Mutex
	seq       uint64

	obsHandler *observability.ObsHandler // nil if proxying to gateway
	obsWriter  *observability.ObsWriter  // nil if proxying to gateway

	baselineStore FlowBaselineStore
	versionStore  VersionHistoryStore
}

func NewServer(managementBaseURL string, cfg ServerConfig) (*Server, error) {
	var parsed *url.URL
	if strings.TrimSpace(managementBaseURL) != "" {
		u, err := url.Parse(strings.TrimRight(managementBaseURL, "/"))
		if err != nil {
			return nil, err
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, errors.New("management url must include scheme and host")
		}
		parsed = u
	}
	targets := cfg.Targets
	if len(targets) == 0 && parsed != nil {
		targets = []Target{{Name: "default", Level: "default", URLs: []string{parsed.String()}}}
	}
	if len(targets) == 0 {
		return nil, errors.New("no deployment targets configured")
	}
	for i := range targets {
		if strings.TrimSpace(targets[i].Name) == "" || strings.TrimSpace(targets[i].Level) == "" || len(targets[i].URLs) == 0 {
			return nil, errors.New("invalid target config")
		}
		for _, raw := range targets[i].URLs {
			u, err := url.Parse(strings.TrimSpace(raw))
			if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
				return nil, errors.New("invalid target url: " + raw)
			}
		}
	}
	srv := &Server{
		config:            cfg,
		managementBaseURL: parsed,
		httpClient:        http.DefaultClient,
		targets:           targets,
		store:             releaseStoreFromConfig(cfg.StoreKind, cfg.StorePath),
		chatStore:         newChatHistoryStore(cfg.StoreKind, cfg.StorePath),
			auditStore:    newAuditStore(cfg.StoreKind, cfg.StorePath),
		baselineStore:     newMemFlowBaselineStore(),
		versionStore:      newMemVersionHistoryStore(),
	}

	if cfg.AuthEnabled {
		srv.userStore = newStudioUserStore(cfg.AuthStorePath, cfg.AuthUsers)
		srv.sessions = newSessionStore()
		srv.tokenStore = newTokenStore(cfg.TokenStorePath, parseEncryptionKey(os.Getenv("RAH_STUDIO_ENCRYPTION_KEY")))
		if srv.userStore != nil && srv.sessions != nil {
			// Invalidate sessions immediately when a user's role or envs change.
			srv.userStore.sessionInvalidator = srv.sessions.deleteByUsername
		}
	}
	if cfg.OIDC != nil && len(cfg.OIDC.Providers) > 0 {
		srv.oidcStateStore = newOIDCStateStore()
	}
	if cfg.Authz != nil {
		srv.authzClient = NewExternalAuthzClient(*cfg.Authz)
	}

	// Gateway service-account credential for management API proxy calls.
	// Separate from Studio users â€” the gateway may have its own auth.
	if u, p := os.Getenv("RAH_GATEWAY_AUTH_USERNAME"), os.Getenv("RAH_GATEWAY_AUTH_PASSWORD"); u != "" && p != "" {
		srv.gatewayBasicCred = base64.StdEncoding.EncodeToString([]byte(u + ":" + p))
	}

	// If an obs store type is configured, create a direct connection to it.
	if strings.TrimSpace(cfg.ObsStoreType) != "" {
		params := observability.ObsStoreParams{
			Type:         cfg.ObsStoreType,
			DSN:          cfg.ObsStoreDSN,
			MaxAccessLog: cfg.ObsMaxLogs,
			MaxTraces:    cfg.ObsMaxTraces,
		}
		obsStore := observability.NewObsStoreFromParams(context.Background(), params)
		// Create a disabled Telemetry for the handler â€” Studio is read-only, it doesn't generate gateway metrics.
		tel := observability.New(observability.Config{Enabled: false})
		srv.obsWriter = observability.NewObsWriter(obsStore, 200, 2*time.Second)
		srv.obsWriter.Start(context.Background())
		srv.obsHandler = observability.NewObsHandler(srv.obsWriter, tel)
	}

	return srv, nil
}

func (s *Server) Handler() http.Handler {
	// apiMux handles all /api/* routes that require authentication.
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/auth/me", s.meHandler)
	apiMux.HandleFunc("/api/auth/change-password", s.changePasswordHandler)
	apiMux.HandleFunc("/api/studio/users", s.studioUsersHandler)
	apiMux.HandleFunc("/api/studio/users/", s.studioUserDeleteHandler)
	apiMux.HandleFunc("/api/schema", s.schemaHandler)
	apiMux.HandleFunc("/api/suggestions", s.suggestionsHandler)
	apiMux.HandleFunc("/api/targets", s.targetsHandler)
	apiMux.HandleFunc("/api/deploy", s.deployHandler)
	apiMux.HandleFunc("/api/releases", s.releasesHandler)
	apiMux.HandleFunc("/api/releases/", s.releaseByIDHandler)
	apiMux.HandleFunc("/api/flow-baselines", s.flowBaselinesHandler)
	apiMux.HandleFunc("/api/flow-baselines/", s.flowBaselineByTagHandler)
	apiMux.HandleFunc("/api/environments/", s.environmentVersionsHandler)
	apiMux.HandleFunc("/api/openapi/import", s.importOpenAPIHandler)
	apiMux.HandleFunc("/api/getAllApis", s.getAllApisProxy)
	apiMux.HandleFunc("/api/sync", s.syncProxy)
	apiMux.HandleFunc("/api/flows/", s.flowsMgmtProxy)
	apiMux.HandleFunc("/api/tenants", s.tenantsMgmtProxy)
	apiMux.HandleFunc("/api/tenants/", s.tenantsMgmtProxy)
	apiMux.HandleFunc("/api/rate-limit-configs", s.rateLimitConfigsMgmtProxy)
	apiMux.HandleFunc("/api/rate-limit-configs/", s.rateLimitConfigsMgmtProxy)
	apiMux.HandleFunc("/api/rate-limit-configs-v2", s.rateLimitConfigsV2MgmtProxy)
	apiMux.HandleFunc("/api/rate-limit-configs-v2/", s.rateLimitConfigsV2MgmtProxy)
	apiMux.HandleFunc("/api/concurrency", s.concurrencyMgmtProxy)
	apiMux.HandleFunc("/api/tiers", s.tiersMgmtProxy)
	apiMux.HandleFunc("/api/tiers/", s.tiersMgmtProxy)
	apiMux.HandleFunc("/api/upstream-services", s.upstreamServicesMgmtProxy)
	apiMux.HandleFunc("/api/upstream-services/", s.upstreamServicesMgmtProxy)
	apiMux.HandleFunc("/api/schedules", s.schedulesMgmtProxy)
	apiMux.HandleFunc("/api/schedules/", s.schedulesMgmtProxy)
	apiMux.HandleFunc("/api/ws/sessions", s.wsSessionsMgmtProxy)
	apiMux.HandleFunc("/api/ws/upstreams", s.wsUpstreamsMgmtProxy)
	apiMux.HandleFunc("/api/ai/chat", s.aiChatHandler)
	apiMux.HandleFunc("/api/ai/project-context", s.aiProjectContextHandler)
	apiMux.HandleFunc("/api/ai/chat-history", s.aiChatHistoryHandler)
	apiMux.HandleFunc("/api/audit", s.auditLogHandler)
	apiMux.HandleFunc("/api/tokens", s.tokenListCreateHandler)
	apiMux.HandleFunc("/api/tokens/", s.tokenRevokeHandler)
	apiMux.HandleFunc("/api/ai/", s.aiMgmtProxy)
	apiMux.HandleFunc("/api/ai", s.aiMgmtProxy)
	apiMux.HandleFunc("/api/cache/", s.cacheMgmtProxy)
	apiMux.HandleFunc("/api/apps", s.appsMgmtProxy)
	apiMux.HandleFunc("/api/apps/", s.appsMgmtProxy)
	apiMux.HandleFunc("/api/grpc/descriptors", s.grpcDescriptorsMgmtProxy)
	apiMux.HandleFunc("/api/grpc/descriptors/", s.grpcDescriptorsMgmtProxy)
	apiMux.HandleFunc("/api/schemas", func(w http.ResponseWriter, r *http.Request) {
		s.proxyPassThrough(w, r, "/schemas")
	})
	apiMux.HandleFunc("/api/schemas/", func(w http.ResponseWriter, r *http.Request) {
		s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
	})
	apiMux.HandleFunc("/api/egress/", func(w http.ResponseWriter, r *http.Request) {
		s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
	})

	// Observability routes: serve from own store if configured, else proxy to gateway.
	// Note: APIDetailHandler and TenantDetailHandler strip the /observability/ prefix;
	// we rewrite the URL path to match what those handlers expect before forwarding.
	if s.obsHandler != nil {
		apiMux.HandleFunc("/api/observability/metrics", s.obsHandler.MetricsHandler)
		apiMux.HandleFunc("/api/observability/access-log", s.obsHandler.AccessLogHandler)
		apiMux.HandleFunc("/api/observability/traces", s.obsHandler.TracesHandler)
		apiMux.HandleFunc("/api/observability/detail-log", s.obsHandler.DetailLogConfigHandler)
		apiMux.HandleFunc("/api/observability/apis", s.obsHandler.APIsHandler)
		apiMux.HandleFunc("/api/observability/apis/", func(w http.ResponseWriter, r *http.Request) {
			// Strip /api prefix so handler sees /observability/apis/{name}
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			s.obsHandler.APIDetailHandler(w, r2)
		})
		apiMux.HandleFunc("/api/observability/tenants/", func(w http.ResponseWriter, r *http.Request) {
			// Strip /api prefix so handler sees /observability/tenants/{alias}
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			s.obsHandler.TenantDetailHandler(w, r2)
		})
		apiMux.HandleFunc("/api/observability/instr-schema", s.obsHandler.InstrSchemaHandler)
	} else {
		apiMux.HandleFunc("/api/observability/", s.obsGatewayProxy)
	}
	// Observability config (GET/POST) â€” always proxy to /debug/observability on the management server.
	// This endpoint is independent of the obs store presence, so it lives outside the if/else above.
	apiMux.HandleFunc("/api/observability/config", func(w http.ResponseWriter, r *http.Request) {
		s.proxyPassThrough(w, r, "/debug/observability")
	})

	// outerMux adds the auth layer:
	//   /api/auth/login  â€” public (credential validation, session creation)
	//   /api/auth/logout â€” public (session deletion, cookie clear)
	//   /api/auth/me     â€” protected (inside apiMux via studioAuthMiddleware)
	//   /api/*           â€” protected via studioAuthMiddleware
	//   /mcp             â€” public (MCP protocol handler; uses its own auth if needed)
	//   /                â€” public (static React SPA â€” login form is rendered client-side)
	outerMux := http.NewServeMux()
	// Auth routes: login + logout are public; change-password and user management are protected.
	outerMux.HandleFunc("/api/auth/login", s.loginHandler)
	outerMux.HandleFunc("/api/auth/logout", s.logoutHandler)
	outerMux.HandleFunc("/api/studio/users/hash", studioUsersHashHandler) // always public (bootstrap helper)
	// OIDC endpoints are public (browser redirects, no session yet during login flow).
	outerMux.HandleFunc("/api/oidc/", s.oidcDispatch)
	outerMux.Handle("/api/", s.studioAuthMiddleware(apiMux))
	outerMux.HandleFunc("/mcp", s.MCPHandler)
	// SCIM endpoints use their own bearer token middleware (not Studio sessions).
	scimMux := http.NewServeMux()
	scimMux.HandleFunc("/scim/v2/Users", s.scimUsersHandler)
	scimMux.HandleFunc("/scim/v2/Users/", s.scimUserByIDHandler)
	scimMux.HandleFunc("/scim/v2/Groups", s.scimGroupsHandler)
	scimMux.HandleFunc("/scim/v2/Groups/", s.scimGroupByIDHandler)
	outerMux.Handle("/scim/", s.scimTokenMiddleware(scimMux))

	// Serve the React SPA from the embedded ui/dist directory.
	// Any path that doesn't match a real file falls back to index.html
	// so that the browser can handle it (no server-side routing needed).
	sub, _ := fs.Sub(uiFS, "ui/dist")
	outerMux.Handle("/", newSPAHandler(http.FS(sub)))
	return outerMux
}

// spaHandler serves static files from fsys, falling back to index.html for
// any path that does not correspond to a real file (SPA client-side routing).
type spaHandler struct{ fsys http.FileSystem }

func newSPAHandler(fsys http.FileSystem) http.Handler { return &spaHandler{fsys: fsys} }

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Attempt to open the requested path as a real file.
	if r.URL.Path != "/" {
		f, err := h.fsys.Open(r.URL.Path)
		if err == nil {
			if err := f.Close(); err != nil {
				log.Printf("studio: failed to close file: %v", err)
			}
			http.FileServer(h.fsys).ServeHTTP(w, r)
			return
		}
	}
	// Fall back to index.html (SPA entry point).
	f, err := h.fsys.Open("index.html")
	if err != nil {
		http.Error(w,
			"UI not built. Run: cd internal/studio/ui && npm install && npm run build",
			http.StatusServiceUnavailable)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("studio: failed to close index.html: %v", err)
		}
	}()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}

func defaultBlocks() []PaletteBlock {
	return []PaletteBlock{
		// â”€â”€ Core â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "generate_tid", Title: "Generate TID", Category: "core", Capability: "tracing",
			Description: "Create a distributed transaction ID and store it in a slot",
			Defaults:    map[string]string{"prefix": "RAH-", "as": "x_tid"},
			Fields: []FieldDef{
				fld("prefix", "Prefix", "String prepended to the generated ID (e.g. RAH- â†’ RAH-00001)", "RAH-"),
				fld("as", "Store as", "Slot name to save the TID into; accessible in later steps as var.<name>", "x_tid"),
			},
		},
		{
			Type: "extract", Title: "Extract Value", Category: "core", Capability: "request-context",
			Description: "Extract a value from header / query / host / body into a slot",
			Defaults:    map[string]string{"condition": "header.X-Tenant || query.t_id || host.subdomain", "as": "tenant_key"},
			Fields: []FieldDef{
				fld("condition", "Source expression", "One or more sources separated by ||; first non-empty value wins. Sources: header.<Name>, query.<key>, host.subdomain, body.<jsonpath>", "header.X-Tenant || query.t_id"),
				fld("as", "Store as", "Slot name to save the extracted value into", "tenant_key"),
			},
		},

		// â”€â”€ Control flow â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "if", Title: "If / Else", Category: "control", Capability: "branching", SupportsNested: true,
			Description: "Branch to one of two sub-flows based on a boolean condition",
			Defaults:    map[string]string{"condition": "", "then": "", "else": ""},
			Fields: []FieldDef{
				fld("condition", "Condition", "Use request data directly: header.X-TID, queryparam.mode, body.userId, path.id. Short forms also work: X-TID auto-qualifies to header.X-TID; mode auto-qualifies to queryparam.mode. Operators: == != > < >= <= && ||", "header.X-TID == \"first\""),
				fld("then", "Then â†’ flow", "Flow to invoke when condition is true. Drag a saved flow from the palette or type a name.", ""),
				fld("else", "Else â†’ flow", "Flow to invoke when condition is false (optional).", ""),
			},
		},
		{
			Type: "switch", Title: "Switch", Category: "control", Capability: "multi-branch", SupportsNested: true,
			Description: "Route to a named sub-flow based on a computed string value",
			Defaults:    map[string]string{"as": "", "cases": ""},
			Fields: []FieldDef{
				fld("as", "Match expression", "What to match against case keys. Use request data directly: header.X-TID, queryparam.mode, body.field. Short forms work: X-TID â†’ header.X-TID, mode â†’ queryparam.mode.", "header.X-TID"),
				fld("cases", "Cases", "Comma-separated key=flow pairs: valid=flow_a,expired=flow_b. Each key is matched exactly against the slot value.", "valid=flow_a,expired=flow_b"),
			},
		},
		{
			Type: "foreach", Title: "For Each", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over a list slot and invoke a sub-flow once per item",
			Defaults:    map[string]string{"source": "", "as": "", "do": ""},
			Fields: []FieldDef{
				fld("source", "Source list", "Slot name containing the list (array) to iterate over", "var.items"),
				fld("as", "Item slot", "Slot name bound to the current iteration item inside the sub-flow", "item"),
				fld("do", "Sub-flow", "Flow name invoked once for each item in the list", "process_item_flow"),
			},
		},
		{
			Type: "call", Title: "Call Flow", Category: "control", Capability: "sub-flow",
			Description: "Invoke a named sub-flow inline â€” like a function call",
			Defaults:    map[string]string{"flow_name": ""},
			Fields: []FieldDef{
				fld("flow_name", "Flow name", "Name of the sub-flow to invoke; it shares the current slot context", "sub_flow"),
			},
		},

		// â”€â”€ HTTP â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "http_call", Title: "HTTP Call", Category: "http", Capability: "upstream",
			Description: "Make an outbound HTTP request and store the response",
			Defaults:    map[string]string{"url": "https://example.com/api", "timeout": "5s", "as": "http_resp"},
			Fields: []FieldDef{
				fld("url", "URL", "Static URL to call (use url_var instead to read the URL from a slot)", "https://example.com/api"),
				fld("url_var", "URL slot", "Slot name holding the dynamic target URL (mutually exclusive with url)", "var.target_url"),
				fld("timeout", "Timeout", "Maximum wait time for the response; e.g. 5s, 500ms, 1m", "5s"),
				fld("as", "Store response as", "Slot name to save the response body string into", "http_resp"),
				fld("retry_condition", "Retry condition", "Boolean expression; when true the request is retried (e.g. status == 503)", "status == 503"),
				fld("max_retries", "Max retries", "Maximum number of retry attempts (default 0 = no retries)", "3"),
			},
		},

		// â”€â”€ Auth â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "token_validation", Title: "Token Validation", Category: "auth", Capability: "jwt-validation",
			Description: "Validate a JWT. Verifies signature (JWKS), standard claims, required scopes, and arbitrary custom claims. Every parameter supports a static value or a runtime variable loaded by any earlier step.",
			Defaults:    map[string]string{"key_identifier": "header.Authorization"},
			Fields: []FieldDef{
				fld("key_identifier", "Token source", "Where to read the token: header.X, query.X, cookie.X, or a variable name", "header.Authorization"),
				fld("input.jwt.jwks_uri", "JWKS URL (static)", "JWKS endpoint URL", "https://YOUR_IDP/.well-known/jwks.json"),
				fld("input.jwt.jwks_uri_var", "JWKS URL (variable)", "Variable holding the JWKS URL (e.g. from load_service_url)", ""),
				fld("input.jwt.alg", "Algorithm (static)", "JWT algorithm. Default: RS256", "RS256"),
				fld("input.jwt.alg_var", "Algorithm (variable)", "Variable holding the algorithm string", ""),
				fld("input.jwt.leeway_seconds", "Leeway seconds (static)", "Clock skew tolerance in seconds. Default: 30", "30"),
				fld("input.jwt.leeway_var", "Leeway seconds (variable)", "Variable holding clock leeway as a number string", ""),
				fld("input.jwt.prefetch_jwks", "Prefetch JWKS", "Pre-warm JWKS cache at deploy time (true/false)", "true"),
				fld("input.jwt.validate", "Validate (static)", "Comma-sep: signature,issuer,audience,expiry,not_before. Empty = all.", "signature,expiry"),
				fld("input.jwt.validate_var", "Validate (variable)", "Variable holding the comma-sep validation check list", ""),
				fld("input.jwt.issuer", "Issuer (static)", "Expected iss claim value", "https://accounts.example.com"),
				fld("input.jwt.issuer_var", "Issuer (variable)", "Variable holding the expected issuer", ""),
				fld("input.jwt.audience", "Audience (static)", "Expected aud claim value", "my-api"),
				fld("input.jwt.audience_var", "Audience (variable)", "Variable holding the expected audience", ""),
				fld("input.jwt.required_scopes", "Required scopes (static)", "Comma-sep scope values that must be present", "read:orders"),
				fld("input.jwt.required_scopes_var", "Required scopes (variable)", "Variable holding comma-sep required scopes", ""),
				fld("input.jwt.scope_claims", "Scope claim keys (static)", "Claim keys to scan for scopes. Default: scope,scp", "scope,scp"),
				fld("input.jwt.scope_claims_var", "Scope claim keys (variable)", "Variable holding the scope claim key list", ""),
				fld("input.jwt.custom_claims", "Custom claims (JSON)", `JSON object of static claim checks e.g. {"role":"admin"}`, `{"role":"admin"}`),
				fld("input.jwt.custom_claims_vars", "Custom claim variables (JSON)", `JSON object mapping claim keys to variable names e.g. {"org":"var.tenant_org"}`, ""),
				fld("input.jwt.on_failure", "On failure mode (static)", `"stop" (return error) or "continue" (write result variable and proceed)`, "stop"),
				fld("input.jwt.on_failure_var", "On failure mode (variable)", "Variable holding 'stop' or 'continue'", ""),
				fld("input.jwt.failure_status", "Failure status (static)", "HTTP status code on failure. Default: 401", "401"),
				fld("input.jwt.failure_status_var", "Failure status (variable)", "Variable holding the failure HTTP status code string", ""),
				fld("input.jwt.failure_body", "Failure body (static)", "Response body on failure. Default: unauthorized", "unauthorized"),
				fld("input.jwt.failure_body_var", "Failure body (variable)", "Variable holding the failure response body", ""),
				fld("input.jwt.result_success", "Success result value (static)", "Value written to result variable on success. Default: true", "true"),
				fld("input.jwt.result_success_var", "Success result value (variable)", "Variable holding the success result value", ""),
				fld("input.jwt.result_failure", "Failure result value (static)", "Value written to result variable on failure. Default: false", "false"),
				fld("input.jwt.result_failure_var", "Failure result value (variable)", "Variable holding the failure result value", ""),
				fld("input.jwt.result_var", "Result variable", "Variable to write result value into (requires on_failure=continue)", ""),
				fld("input.jwt.claims_var", "Claims output variable", "Variable to write all JWT claims JSON into on success", ""),
				fld("input.jwt.subject_var", "Subject output variable", "Variable to write the JWT sub (subject) claim into on success", ""),
				fld("input.jwt.client_id_var", "Client ID output variable", "Variable to write the client_id (or azp/appid) claim into on success", ""),
				fld("input.jwt.scopes_out_var", "Scopes output variable", "Variable to write comma-separated parsed scopes into on success", ""),
			},
		},
		{
			Type: "registry_lookup", Title: "Registry Lookup", Category: "auth", Capability: "key-lookup",
			Description: "Fetch a config entry from the key registry and store it in a slot",
			Defaults:    map[string]string{"key_identifier": "var.tenant_key", "as": "config", "scope": "tenant"},
			Fields: []FieldDef{
				fld("key_identifier", "Key slot", "Slot whose value is used as the registry lookup key", "var.tenant_key"),
				fld("as", "Store as", "Slot name to save the looked-up config value into", "config"),
				fld("scope", "Scope", "Registry namespace / scope to search within (e.g. tenant, global)", "tenant"),
			},
		},

		// â”€â”€ String ops â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "concat", Title: "Concat", Category: "string", Capability: "string-op",
			Description: "Concatenate two string slots (with optional separator) and store the result",
			Defaults:    map[string]string{"source": "var.prefix", "value": "var.suffix", "as": "result", "key_identifier": ""},
			Fields: []FieldDef{
				fld("source", "Left string", "Slot name (or literal) providing the first part of the concatenation", "var.prefix"),
				fld("value", "Right string", "Slot name (or literal) providing the second part of the concatenation", "var.suffix"),
				fld("key_identifier", "Separator", "String inserted between the two parts (leave blank for direct join)", ""),
				fld("as", "Store as", "Slot name to save the concatenated result into", "result"),
			},
		},
		{
			Type: "to_lower", Title: "To Lower", Category: "string", Capability: "string-op",
			Description: "Convert a string slot to lower-case",
			Defaults:    map[string]string{"source": "var.input", "as": "lower_val"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot name containing the string to convert", "var.input"),
				fld("as", "Store as", "Slot name to save the lower-cased result into", "lower_val"),
			},
		},
		{
			Type: "to_upper", Title: "To Upper", Category: "string", Capability: "string-op",
			Description: "Convert a string slot to upper-case",
			Defaults:    map[string]string{"source": "var.input", "as": "upper_val"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot name containing the string to convert", "var.input"),
				fld("as", "Store as", "Slot name to save the upper-cased result into", "upper_val"),
			},
		},
		{
			Type: "substring", Title: "Substring", Category: "string", Capability: "string-op",
			Description: "Slice a string slot; provide optional start/end index parameters",
			Defaults:    map[string]string{"source": "var.input", "as": "sliced"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot name containing the string to slice", "var.input"),
				fld("as", "Store as", "Slot name to save the substring result into", "sliced"),
			},
		},
		{
			Type: "to_int", Title: "To Int", Category: "string", Capability: "type-convert",
			Description: "Parse a string slot as a 64-bit integer",
			Defaults:    map[string]string{"source": "var.str_val", "as": "int_val"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot name containing the string to parse as an integer", "var.str_val"),
				fld("as", "Store as", "Slot name to save the parsed integer into", "int_val"),
			},
		},

		// â”€â”€ Math â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "add", Title: "Add", Category: "math", Capability: "arithmetic",
			Description: "Add two numeric slots (key_identifier + source) and store the result",
			Defaults:    map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []FieldDef{
				fld("key_identifier", "Left operand slot", "Slot name containing the first (left) number", "var.a"),
				fld("source", "Right operand slot", "Slot name containing the second (right) number", "var.b"),
				fld("as", "Store as", "Slot name to save the arithmetic result into", "result"),
			},
		},
		{
			Type: "sub", Title: "Subtract", Category: "math", Capability: "arithmetic",
			Description: "Subtract source slot from key_identifier slot and store the result",
			Defaults:    map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []FieldDef{
				fld("key_identifier", "Left operand slot", "Slot name containing the number to subtract from", "var.a"),
				fld("source", "Right operand slot", "Slot name containing the number to subtract", "var.b"),
				fld("as", "Store as", "Slot name to save the arithmetic result into", "result"),
			},
		},
		{
			Type: "mul", Title: "Multiply", Category: "math", Capability: "arithmetic",
			Description: "Multiply two numeric slots and store the result",
			Defaults:    map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []FieldDef{
				fld("key_identifier", "Left operand slot", "Slot name containing the first factor", "var.a"),
				fld("source", "Right operand slot", "Slot name containing the second factor", "var.b"),
				fld("as", "Store as", "Slot name to save the arithmetic result into", "result"),
			},
		},
		{
			Type: "div", Title: "Divide", Category: "math", Capability: "arithmetic",
			Description: "Divide key_identifier slot by source slot and store the result",
			Defaults:    map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []FieldDef{
				fld("key_identifier", "Dividend slot", "Slot name containing the number to be divided", "var.a"),
				fld("source", "Divisor slot", "Slot name containing the number to divide by", "var.b"),
				fld("as", "Store as", "Slot name to save the arithmetic result into", "result"),
			},
		},

		// â”€â”€ Response â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "set_response_header", Title: "Set Response Header", Category: "response", Capability: "response-mod",
			Description: "Set a response header to the value from a slot",
			Defaults:    map[string]string{"key": "X-Custom-Header", "source": "var.header_value"},
			Fields: []FieldDef{
				fld("key", "Header name", "Name of the HTTP response header to set (e.g. X-Request-ID)", "X-Custom-Header"),
				fld("source", "Value slot", "Slot name whose string value is written to the response header", "var.header_value"),
			},
		},
		{
			Type: "set_response_body", Title: "Set Response Body", Category: "response", Capability: "response-mod",
			Description: "Replace the response body with the value from a slot",
			Defaults:    map[string]string{"source": "var.body"},
			Fields: []FieldDef{
				fld("source", "Body slot", "Slot name whose value becomes the response body (string or JSON)", "var.body"),
			},
		},
		{
			Type: "set_response_status", Title: "Set Response Status", Category: "response", Capability: "response-mod",
			Description: "Set the HTTP response status code",
			Defaults:    map[string]string{"value": "200"},
			Fields: []FieldDef{
				fld("value", "Status code", "Numeric HTTP status code to send in the response (e.g. 200, 401, 503)", "200"),
			},
		},
		{
			Type: "echo_request", Title: "Echo Request", Category: "response", Capability: "debug",
			Description: "Mirror the incoming request back as the response â€” useful for debugging flows",
			Defaults:    map[string]string{},
			Fields:      []FieldDef{},
		},

		// â”€â”€ Encoding â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "base64_encode", Title: "Base64 Encode", Category: "encoding", Capability: "encoding",
			Description: "Encode a byte slot to base64. Variant: std (default), url, raw_url, raw_std.",
			Defaults: map[string]string{"source": "var.input", "as": "encoded"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing bytes to encode", "var.input"),
				fld("as", "Store as", "Slot for the base64 output", "encoded"),
				fld("input.encoding", "Encoding variant", "std | url | raw_url | raw_std (default: std)", "std"),
			},
		},
		{
			Type: "base64_decode", Title: "Base64 Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a base64 string slot into raw bytes. Default variant: raw_url (JWT-friendly). Clears result on invalid input.",
			Defaults: map[string]string{"source": "var.encoded", "as": "decoded"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the base64 string", "var.encoded"),
				fld("as", "Store as", "Slot for the decoded bytes", "decoded"),
				fld("input.encoding", "Encoding variant", "std | url | raw_url | raw_std (default: raw_url)", "raw_url"),
			},
		},
		{
			Type: "hex_encode", Title: "Hex Encode", Category: "encoding", Capability: "encoding",
			Description: "Encode a byte slot as a lowercase hexadecimal string.",
			Defaults: map[string]string{"source": "var.input", "as": "hex"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing bytes to encode", "var.input"),
				fld("as", "Store as", "Slot for the hex string", "hex"),
			},
		},
		{
			Type: "hex_decode", Title: "Hex Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a hex string slot into raw bytes. Clears result on invalid input.",
			Defaults: map[string]string{"source": "var.hex", "as": "decoded"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the hex string", "var.hex"),
				fld("as", "Store as", "Slot for the decoded bytes", "decoded"),
			},
		},
		{
			Type: "url_encode", Title: "URL Encode", Category: "encoding", Capability: "encoding",
			Description: "Percent-encode a string slot (RFC 3986). Space â†’ %20. Unreserved chars pass through.",
			Defaults: map[string]string{"source": "var.input", "as": "encoded"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the string to encode", "var.input"),
				fld("as", "Store as", "Slot for the percent-encoded output", "encoded"),
			},
		},
		{
			Type: "url_decode", Title: "URL Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a percent-encoded string slot. '+' is decoded as space.",
			Defaults: map[string]string{"source": "var.encoded", "as": "decoded"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the percent-encoded string", "var.encoded"),
				fld("as", "Store as", "Slot for the decoded output", "decoded"),
			},
		},

		// â”€â”€ Crypto / Hash â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			Type: "sha256_hash", Title: "SHA-256 Hash", Category: "crypto", Capability: "hashing",
			Description: "Compute SHA-256 of a slot. Output is a lowercase hex string. No key â€” use hmac_sha256 for signed hashes.",
			Defaults: map[string]string{"source": "var.input", "as": "digest"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the data to hash", "var.input"),
				fld("as", "Store as", "Slot for the SHA-256 hex string", "digest"),
			},
		},
		{
			Type: "hmac_sha256", Title: "HMAC-SHA256", Category: "crypto", Capability: "signing",
			Description: "Compute HMAC-SHA256 using a bake-time secret key. Output is a lowercase hex string. Used for webhook signatures, request signing.",
			Defaults: map[string]string{"source": "var.payload", "as": "signature"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the data to sign", "var.payload"),
				fld("as", "Store as", "Slot for the HMAC hex string", "signature"),
				fld("input.key", "Secret key", "Static HMAC key (baked at compile time â€” store in secrets manager for production)", ""),
			},
		},
		{
			Type: "hmac_sha1", Title: "HMAC-SHA1", Category: "crypto", Capability: "signing",
			Description: "Compute HMAC-SHA1 using a bake-time key. Output is a lowercase hex string. Legacy integrations only.",
			Defaults: map[string]string{"source": "var.payload", "as": "signature"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the data to sign", "var.payload"),
				fld("as", "Store as", "Slot for the HMAC hex string", "signature"),
				fld("input.key", "Secret key", "Static HMAC key (baked at compile time)", ""),
			},
		},
		{
			Type: "md5_hash", Title: "MD5 Hash", Category: "crypto", Capability: "hashing",
			Description: "Compute MD5 of a slot. Output is a lowercase hex string. Cryptographically broken â€” use for checksums or legacy compatibility only.",
			Defaults: map[string]string{"source": "var.input", "as": "digest"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the data to hash", "var.input"),
				fld("as", "Store as", "Slot for the MD5 hex string", "digest"),
			},
		},
		{
			Type: "aes_encrypt", Title: "AES Encrypt (GCM)", Category: "crypto", Capability: "encryption",
			Description: "Encrypt a slot with AES-GCM. Key is hex-encoded (32 chars = AES-128, 64 = AES-256). Output is nonce||ciphertext.",
			Defaults: map[string]string{"source": "var.plaintext", "as": "ciphertext"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing the plaintext", "var.plaintext"),
				fld("as", "Store as", "Slot for nonce||ciphertext output", "ciphertext"),
				fld("input.key", "Key (hex)", "AES key as hex: 32 chars = AES-128, 48 = AES-192, 64 = AES-256", ""),
			},
		},
		{
			Type: "aes_decrypt", Title: "AES Decrypt (GCM)", Category: "crypto", Capability: "encryption",
			Description: "Decrypt AES-GCM ciphertext (nonce||ciphertext). Sets Failed=true on auth failure.",
			Defaults: map[string]string{"source": "var.ciphertext", "as": "plaintext"},
			Fields: []FieldDef{
				fld("source", "Source slot", "Slot containing nonce||ciphertext", "var.ciphertext"),
				fld("as", "Store as", "Slot for the decrypted plaintext", "plaintext"),
				fld("input.key", "Key (hex)", "Same AES key used during encryption", ""),
			},
		},
	}
}

func (s *Server) schemaHandler(w http.ResponseWriter, r *http.Request) {
	// Prefer live step catalog from the management server.
	// This means adding a step to the compiler + step_descriptors.go is
	// sufficient â€” the Studio palette updates automatically on next load.
	if s.managementBaseURL != nil {
		targetURL, err := buildTargetURL(s.managementBaseURL.String(), "/meta/steps", "")
		if err == nil {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
			if err == nil {
				resp, err := s.httpClient.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					defer resp.Body.Close()
					w.Header().Set("Content-Type", "application/json")
					io.Copy(w, resp.Body) //nolint:errcheck
					return
				}
				if resp != nil {
					resp.Body.Close()
				}
			}
		}
		// Fall through to local defaults if the management server is unreachable.
	}

	// Fallback: local hardcoded palette (standalone / dev mode).
	blocks := defaultBlocks()
	seen := map[string]struct{}{}
	cats := make([]string, 0)
	for _, b := range blocks {
		if _, ok := seen[b.Category]; !ok {
			seen[b.Category] = struct{}{}
			cats = append(cats, b.Category)
		}
	}
	sort.Strings(cats)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SchemaResponse{Version: "v2-modular-ui", Categories: cats, Blocks: blocks})
}

func (s *Server) suggestionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stepType := r.URL.Query().Get("step_type")
	resp := SuggestionResponse{StepType: stepType, Notes: []string{"Use sections separately: flows, APIs, releases/deployments."}}
	switch stepType {
	case "if":
		resp.SuggestedFlows = []string{"jwt_validation", "legacy_auth"}
		resp.SuggestedBlocks = []string{"switch", "proxy"}
	default:
		resp.SuggestedFlows = []string{"routing_logic"}
		resp.SuggestedBlocks = []string{"proxy"}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) targetsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	releases, _ := s.store.List(context.Background())
	s.historyMu.RLock()
	hist := append([]DeployRecord(nil), s.history...)
	s.historyMu.RUnlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"targets": s.targets, "history": hist, "releases": releases, "stores_supported": []string{"memory", "file"}})
}

func (s *Server) importOpenAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req OpenAPIImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	apis, source, err := parseOpenAPISpec(req.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(OpenAPIImportResponse{Source: source, APIs: apis})
}

func parseOpenAPISpec(spec string) ([]ImportedAPI, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, "", errors.New("spec is required")
	}
	var data map[string]any
	source := "json"
	if err := json.Unmarshal([]byte(spec), &data); err != nil {
		// Try YAML â†’ map conversion for full schema extraction
		yamlData, yamlErr := yamlToMap(spec)
		if yamlErr != nil {
			// Fall back to the line-scanner YAML parser (paths/methods only)
			apis, err2 := parseOpenAPIYAML(spec)
			if err2 != nil {
				return nil, "", errors.New("spec must be valid OpenAPI JSON or YAML")
			}
			return apis, "yaml", nil
		}
		data = yamlData
		source = "yaml"
	}

	pathsRaw, ok := data["paths"].(map[string]any)
	if !ok {
		return nil, source, errors.New("openapi spec missing paths object")
	}

	// Extract top-level components for $ref resolution.
	components, _ := data["components"].(map[string]any)

	apis := make([]ImportedAPI, 0)
	for p, methodsRaw := range pathsRaw {
		methodsMap, ok := methodsRaw.(map[string]any)
		if !ok {
			continue
		}
		for m, opRaw := range methodsMap {
			ml := strings.ToUpper(m)
			switch ml {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions:
			default:
				continue
			}
			opMap, _ := opRaw.(map[string]any)
			name := strings.ToLower(ml) + "_" + strings.ReplaceAll(strings.Trim(p, "/"), "/", "_")
			if opMap != nil {
				if opID, ok := opMap["operationId"].(string); ok && strings.TrimSpace(opID) != "" {
					name = opID
				}
			}
			api := ImportedAPI{Name: name, Path: p, Method: ml}
			if opMap != nil {
				api.ValidateRouteRules = extractValidationRules(opMap, components)
			}
			apis = append(apis, api)
		}
	}
	sort.Slice(apis, func(i, j int) bool {
		if apis[i].Path == apis[j].Path {
			return apis[i].Method < apis[j].Method
		}
		return apis[i].Path < apis[j].Path
	})
	return apis, source, nil
}

// yamlToMap unmarshals a YAML string into a generic map using yaml.v3.
func yamlToMap(spec string) (map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal([]byte(spec), &raw); err != nil {
		return nil, err
	}
	return normalizeYAMLMap(raw)
}

// normalizeYAMLMap converts yaml.v3 map[string]any / map[any]any trees into
// map[string]any so they can be consumed the same way as JSON-decoded data.
func normalizeYAMLMap(v any) (map[string]any, error) {
	out, err := deepNormalize(v)
	if err != nil {
		return nil, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		return nil, errors.New("yaml root is not a mapping")
	}
	return m, nil
}

func deepNormalize(v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			n, err := deepNormalize(vv)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			ks := fmt.Sprintf("%v", k)
			n, err := deepNormalize(vv)
			if err != nil {
				return nil, err
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			n, err := deepNormalize(vv)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return val, nil
	}
}

// resolveRef follows a $ref pointer (e.g. "#/components/schemas/Foo") within
// the same document using the provided components map.
func resolveRef(ref string, components map[string]any) map[string]any {
	// Only support local refs: "#/components/schemas/..." and "#/components/requestBodies/..."
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) || components == nil {
		return nil
	}
	rest := strings.TrimPrefix(ref, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	section, ok := components[parts[0]].(map[string]any)
	if !ok {
		return nil
	}
	target, ok := section[parts[1]].(map[string]any)
	if !ok {
		return nil
	}
	return target
}

// extractValidationRules parses an OpenAPI operation object and returns a list
// of validate_route rules for required fields, patterns, and enum constraints.
func extractValidationRules(operation map[string]any, components map[string]any) []openAPIRuleConfig {
	// Navigate: requestBody â†’ content â†’ application/json â†’ schema
	reqBody, _ := operation["requestBody"].(map[string]any)
	if reqBody == nil {
		return nil
	}
	content, _ := reqBody["content"].(map[string]any)
	if content == nil {
		return nil
	}
	jsonContent, _ := content["application/json"].(map[string]any)
	if jsonContent == nil {
		return nil
	}
	schema, _ := jsonContent["schema"].(map[string]any)
	if schema == nil {
		return nil
	}
	// Resolve top-level $ref if present
	if ref, ok := schema["$ref"].(string); ok {
		resolved := resolveRef(ref, components)
		if resolved == nil {
			return nil
		}
		schema = resolved
	}

	var rules []openAPIRuleConfig
	collectSchemaRules(schema, "", components, &rules)
	return rules
}

// collectSchemaRules recursively extracts validation rules from a JSON Schema object.
// prefix is the dot-notation path prefix for nested objects (empty at top level).
func collectSchemaRules(schema map[string]any, prefix string, components map[string]any, rules *[]openAPIRuleConfig) {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return
	}

	// Build required set
	requiredSet := map[string]bool{}
	if reqArr, ok := schema["required"].([]any); ok {
		for _, r := range reqArr {
			if s, ok := r.(string); ok {
				requiredSet[s] = true
			}
		}
	}

	// Sort property names for deterministic output
	propNames := make([]string, 0, len(properties))
	for k := range properties {
		propNames = append(propNames, k)
	}
	sort.Strings(propNames)

	for _, propName := range propNames {
		propRaw := properties[propName]
		prop, _ := propRaw.(map[string]any)
		if prop == nil {
			continue
		}
		// Resolve $ref within the property
		if ref, ok := prop["$ref"].(string); ok {
			resolved := resolveRef(ref, components)
			if resolved != nil {
				prop = resolved
			}
		}

		fieldPath := propName
		if prefix != "" {
			fieldPath = prefix + "." + propName
		}

		propType, _ := prop["type"].(string)

		// Rule 1: required field â†’ exists check
		if requiredSet[propName] {
			*rules = append(*rules, openAPIRuleConfig{
				Label: "require " + fieldPath,
				When: openAPICondConfig{
					Source: "req_body",
					Path:   fieldPath,
					Check:  "missing",
				},
				OnMatch: openAPIOnMatchConfig{
					Dest:    "fail",
					Status:  400,
					Message: "missing required field: " + fieldPath,
				},
			})
		}

		// Rule 2: enum constraint â†’ in check
		if enumRaw, ok := prop["enum"].([]any); ok && len(enumRaw) > 0 {
			vals := make([]string, 0, len(enumRaw))
			for _, e := range enumRaw {
				vals = append(vals, fmt.Sprintf("%v", e))
			}
			*rules = append(*rules, openAPIRuleConfig{
				Label: "enum " + fieldPath,
				When: openAPICondConfig{
					Op: "and",
					Children: []openAPICondConfig{
						{Source: "req_body", Path: fieldPath, Check: "exists"},
						{Source: "req_body", Path: fieldPath, Check: "not_in", InValues: vals},
					},
				},
				OnMatch: openAPIOnMatchConfig{
					Dest:    "fail",
					Status:  400,
					Message: "invalid value for field: " + fieldPath,
				},
			})
		}

		// Rule 3: pattern constraint â†’ regex check
		if pattern, ok := prop["pattern"].(string); ok && pattern != "" {
			*rules = append(*rules, openAPIRuleConfig{
				Label: "pattern " + fieldPath,
				When: openAPICondConfig{
					Op: "and",
					Children: []openAPICondConfig{
						{Source: "req_body", Path: fieldPath, Check: "exists"},
						{Source: "req_body", Path: fieldPath, Check: "not_regex", Value: pattern},
					},
				},
				OnMatch: openAPIOnMatchConfig{
					Dest:    "fail",
					Status:  400,
					Message: "invalid value for field: " + fieldPath,
				},
			})
		}

		// Rule 4: recurse into nested objects
		if propType == "object" {
			nestedSchema := prop
			// Resolve nested $ref if needed
			if ref, ok := nestedSchema["$ref"].(string); ok {
				if resolved := resolveRef(ref, components); resolved != nil {
					nestedSchema = resolved
				}
			}
			collectSchemaRules(nestedSchema, fieldPath, components, rules)
		}
	}
}

func parseOpenAPIYAML(spec string) ([]ImportedAPI, error) {
	lines := strings.Split(spec, "\n")
	inPaths := false
	pathsIndent := -1
	pathIndent := -1
	methodIndent := -1
	currentPath := ""
	currentMethod := ""
	apis := make([]ImportedAPI, 0)

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if strings.HasPrefix(trim, "paths:") {
			inPaths = true
			pathsIndent = indent
			continue
		}
		if !inPaths {
			continue
		}
		if indent <= pathsIndent {
			// exited paths block
			break
		}

		if strings.HasPrefix(trim, "/") && strings.HasSuffix(trim, ":") {
			if pathIndent == -1 {
				pathIndent = indent
			}
			if indent == pathIndent {
				currentPath = strings.TrimSuffix(trim, ":")
				currentMethod = ""
				continue
			}
		}
		if currentPath == "" {
			continue
		}

		if strings.HasSuffix(trim, ":") {
			m := strings.ToUpper(strings.TrimSuffix(trim, ":"))
			switch m {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions:
				if methodIndent == -1 {
					methodIndent = indent
				}
				if indent == methodIndent {
					currentMethod = m
					name := strings.ToLower(m) + "_" + strings.ReplaceAll(strings.Trim(currentPath, "/"), "/", "_")
					apis = append(apis, ImportedAPI{Name: name, Path: currentPath, Method: m})
					continue
				}
			}
		}

		if currentMethod != "" && strings.HasPrefix(trim, "operationId:") {
			op := strings.TrimSpace(strings.TrimPrefix(trim, "operationId:"))
			op = strings.Trim(op, "\"'")
			if op != "" && len(apis) > 0 {
				apis[len(apis)-1].Name = op
			}
		}
	}

	if len(apis) == 0 {
		return nil, errors.New("openapi yaml paths not found")
	}
	sort.Slice(apis, func(i, j int) bool {
		if apis[i].Path == apis[j].Path {
			return apis[i].Method < apis[j].Method
		}
		return apis[i].Path < apis[j].Path
	})
	return apis, nil
}

func (s *Server) deployHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	rec, err := s.resolveRelease(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	selected := s.selectTargets(req.Levels, req.TargetNames)
	if len(selected) == 0 {
		http.Error(w, "no matching targets", http.StatusBadRequest)
		return
	}

	results := make([]DeployResult, 0)
	for _, t := range selected {
		for _, raw := range t.URLs {
			s.preSyncDeploy(r.Context(), raw, rec.Payload)
			targetURL, err := buildTargetURL(raw, "/sync", "")
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: raw, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			hReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(rec.Payload))
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			hReq.Header.Set("Content-Type", "application/json")
			resp, err := s.httpClient.Do(hReq)
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Status: resp.StatusCode})
		}
	}
	s.historyMu.Lock()
	s.history = append([]DeployRecord{{At: nowJSON(), ReleaseID: rec.ReleaseID, Levels: req.Levels, Targets: req.TargetNames, Results: results}}, s.history...)
	if len(s.history) > 200 {
		s.history = s.history[:200]
	}
	s.historyMu.Unlock()
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	go s.auditStore.Append(context.Background(), AuditRecord{
		ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
		Actor: actor, Action: "deploy", ResourceType: "release",
		ResourceID: rec.ReleaseID, Status: "success",
		Summary: fmt.Sprintf("%s deployed release %s", actor, rec.ReleaseID),
	})
	_ = json.NewEncoder(w).Encode(map[string]any{"release_id": rec.ReleaseID, "results": results})
}

func (s *Server) resolveRelease(req DeployRequest) (ReleaseRecord, error) {
	rid := strings.TrimSpace(req.ReleaseID)
	if len(req.Payload) > 0 {
		if rid == "" {
			rid = s.nextReleaseID()
		}
		rec := ReleaseRecord{ReleaseID: rid, CreatedAt: nowJSON(), InstructionSetVersion: strings.TrimSpace(req.InstructionSetVersion), APIVersions: req.APIVersions, Payload: req.Payload}
		if err := s.store.Put(context.Background(), rec); err != nil {
			return ReleaseRecord{}, err
		}
		return rec, nil
	}
	if rid == "" {
		return ReleaseRecord{}, errors.New("payload or release_id is required")
	}
	return s.store.Get(context.Background(), rid)
}

func (s *Server) nextReleaseID() string {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	s.seq++
	return "rel-" + strconv.FormatUint(s.seq, 10)
}

func (s *Server) selectTargets(levels, names []string) []Target {
	ls, ns := map[string]struct{}{}, map[string]struct{}{}
	for _, l := range levels {
		if t := strings.TrimSpace(l); t != "" {
			ls[t] = struct{}{}
		}
	}
	for _, n := range names {
		if t := strings.TrimSpace(n); t != "" {
			ns[t] = struct{}{}
		}
	}
	out := make([]Target, 0)
	for _, t := range s.targets {
		_, okL := ls[t.Level]
		_, okN := ns[t.Name]
		if (len(ls) == 0 && len(ns) == 0) || okL || okN {
			out = append(out, t)
		}
	}
	return out
}

func buildTargetURL(baseRaw, endpointPath, rawQuery string) (string, error) {
	baseURL, err := url.Parse(strings.TrimSpace(baseRaw))
	if err != nil || baseURL == nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return "", errors.New("invalid target url")
	}
	joined := *baseURL
	joined.Path = joinURLPath(baseURL.Path, endpointPath)
	joined.RawQuery = rawQuery
	return joined.String(), nil
}

func joinURLPath(basePath, endpointPath string) string {
	if basePath == "" || basePath == "/" {
		if strings.HasPrefix(endpointPath, "/") {
			return endpointPath
		}
		return "/" + endpointPath
	}
	b := strings.TrimRight(basePath, "/")
	e := strings.TrimLeft(endpointPath, "/")
	return b + "/" + e
}

// resolveKeyRef resolves a secret reference to a plaintext string.
// Supports "env:VAR_NAME", "file:///path", or a literal value.
func resolveKeyRef(ref string) (string, error) {
	if after, ok := strings.CutPrefix(ref, "env:"); ok {
		val := os.Getenv(after)
		if val == "" {
			return "", fmt.Errorf("env var %q is not set", after)
		}
		return val, nil
	}
	if after, ok := strings.CutPrefix(ref, "file://"); ok {
		data, err := os.ReadFile(after)
		if err != nil {
			return "", fmt.Errorf("read key file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return ref, nil
}

// findOrCreateApp returns the AppID for the named app, creating it if absent.
// Returns 0 on failure.
func (s *Server) findOrCreateApp(ctx context.Context, targetBase, appName string) uint32 {
	listURL, err := buildTargetURL(targetBase, "/apps", "")
	if err != nil {
		return 0
	}
	data, status, err := s.gatewayCall(ctx, http.MethodGet, listURL, nil)
	if err != nil || status != http.StatusOK {
		return 0
	}
	var resp struct {
		Items []struct {
			AppID uint32 `json:"app_id"`
			Name  string `json:"name"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &resp) == nil {
		for _, app := range resp.Items {
			if app.Name == appName {
				return app.AppID
			}
		}
	}
	body, _ := json.Marshal(map[string]string{"name": appName})
	createURL, err := buildTargetURL(targetBase, "/apps", "")
	if err != nil {
		return 0
	}
	data, status, err = s.gatewayCall(ctx, http.MethodPost, createURL, body)
	if err != nil || status != http.StatusOK {
		return 0
	}
	var created struct {
		AppID uint32 `json:"app_id"`
	}
	if json.Unmarshal(data, &created) == nil {
		return created.AppID
	}
	return 0
}

// resolveTenantIDs resolves tenant aliases to numeric IDs via the gateway.
func (s *Server) resolveTenantIDs(ctx context.Context, targetBase string, aliases []string) []uint16 {
	var ids []uint16
	for _, alias := range aliases {
		u, err := buildTargetURL(targetBase, "/tenants/"+alias, "")
		if err != nil {
			continue
		}
		data, status, err := s.gatewayCall(ctx, http.MethodGet, u, nil)
		if err != nil || status != http.StatusOK {
			continue
		}
		var rec struct {
			TenantID uint16 `json:"tenant_id"`
		}
		if json.Unmarshal(data, &rec) == nil && rec.TenantID != 0 {
			ids = append(ids, rec.TenantID)
		}
	}
	return ids
}

func methodToAction(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

func (s *Server) getAllApisProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyToDefault(w, r, http.MethodGet, "/getAllApis")
}
func (s *Server) flowsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}
func (s *Server) syncProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	name := s.config.SandboxDeployment
	if name == "" {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		s.proxyToDefault(rec, r, http.MethodPost, "/sync")
		status := "success"
		if rec.status >= 400 {
			status = "failure"
		}
		go s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: actor, Action: "sync", ResourceType: "flow",
			Status: status, Summary: actor + " synced flows",
		})
		return
	}
	dep := findDeployment(s.config.Deployments, name)
	if dep == nil {
		http.Error(w, "sandbox_deployment '"+name+"' not found in deployments config", http.StatusInternalServerError)
		return
	}
	if len(dep.Targets) == 0 {
		http.Error(w, "sandbox deployment '"+name+"' has no targets configured", http.StatusInternalServerError)
		return
	}
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	s.proxyToSandbox(rec, r, dep.Targets)
	status := "success"
	if rec.status >= 400 {
		status = "failure"
	}
	go s.auditStore.Append(context.Background(), AuditRecord{
		ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
		Actor: actor, Action: "sync", ResourceType: "flow",
		Status: status, Summary: actor + " synced flows",
	})
}

// proxyToSandbox forwards POST /api/sync to every target in the sandbox deployment.
// All targets must succeed; the first non-2xx response short-circuits and is returned as-is.
func (s *Server) proxyToSandbox(w http.ResponseWriter, r *http.Request, targets []string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	var lastStatus int
	var lastBody []byte
	for _, raw := range targets {
		targetURL, err := buildTargetURL(raw, "/sync", r.URL.RawQuery)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			http.Error(w, "invalid sandbox target url: "+raw, http.StatusBadGateway)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(body))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			http.Error(w, "failed to build sandbox request", http.StatusInternalServerError)
			return
		}
		req.Header = r.Header.Clone()
		if s.gatewayBasicCred != "" {
			req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			http.Error(w, "sandbox target unreachable: "+raw, http.StatusBadGateway)
			return
		}
		lastBody, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		if lastStatus >= 300 {
			for k, vals := range resp.Header {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(lastStatus)
			_, _ = w.Write(lastBody)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(lastStatus)
	_, _ = w.Write(lastBody)
}

// tenantsMgmtProxy forwards /api/tenants[/...] â†’ /tenants[/...] on the management server.
func (s *Server) tenantsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/api")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.proxyPassThrough(w, r, targetPath)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	action := methodToAction(r.Method)
	s.recordingProxy(w, r, targetPath, actor, "tenant", "tenant."+action)
}

// rateLimitConfigsMgmtProxy forwards /api/rate-limit-configs[/...] â†’ /rate-limit-configs[/...].
func (s *Server) rateLimitConfigsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/api")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.proxyPassThrough(w, r, targetPath)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	action := methodToAction(r.Method)
	s.recordingProxy(w, r, targetPath, actor, "ratelimit_config", "ratelimit_config."+action)
}

// cacheMgmtProxy forwards /api/cache/{alias}/{key} â†’ /cache/{alias}/{key} on the management server.
func (s *Server) cacheMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// rateLimitConfigsV2MgmtProxy forwards /api/rate-limit-configs-v2[/...] â†’ /rate-limit-configs-v2[/...].
func (s *Server) rateLimitConfigsV2MgmtProxy(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/api")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.proxyPassThrough(w, r, targetPath)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	action := methodToAction(r.Method)
	s.recordingProxy(w, r, targetPath, actor, "ratelimit_config_v2", "ratelimit_config_v2."+action)
}

// concurrencyMgmtProxy forwards /api/concurrency â†’ /admin/concurrency on the management server.
func (s *Server) concurrencyMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, "/admin/concurrency")
}

// tiersMgmtProxy forwards /api/tiers[/...] â†’ /tiers[/...] on the management server.
func (s *Server) tiersMgmtProxy(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/api")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.proxyPassThrough(w, r, targetPath)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	action := methodToAction(r.Method)
	s.recordingProxy(w, r, targetPath, actor, "tier", "tier."+action)
}

// upstreamServicesMgmtProxy forwards /api/upstream-services[/...] â†’ /upstream-services[/...].
func (s *Server) upstreamServicesMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// schedulesMgmtProxy forwards /api/schedules[/...] â†’ /schedules[/...] on the management server.
func (s *Server) schedulesMgmtProxy(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/api")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.proxyPassThrough(w, r, targetPath)
		return
	}
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	action := methodToAction(r.Method)
	s.recordingProxy(w, r, targetPath, actor, "schedule", "schedule."+action)
}

// wsSessionsMgmtProxy forwards /api/ws/sessions â†’ /ws/sessions on the management server.
func (s *Server) wsSessionsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, "/ws/sessions")
}

// wsUpstreamsMgmtProxy forwards /api/ws/upstreams â†’ /ws/upstreams on the management server.
func (s *Server) wsUpstreamsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, "/ws/upstreams")
}

// aiMgmtProxy forwards /api/ai[/...] â†’ /ai[/...] on the management server.
func (s *Server) aiMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// appsMgmtProxy forwards /api/apps[/...] â†’ /apps[/...] on the management server.
func (s *Server) appsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// grpcDescriptorsMgmtProxy forwards /api/grpc/descriptors[/...] â†’ /grpc/descriptors[/...].
func (s *Server) grpcDescriptorsMgmtProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// obsGatewayProxy forwards /api/observability/[...] â†’ /observability/[...] on the management server.
// Used as fallback when no direct obs store is configured.
func (s *Server) obsGatewayProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyPassThrough(w, r, strings.TrimPrefix(r.URL.Path, "/api"))
}

// proxyPassThrough forwards the request as-is (any method, with body and query) to the
// management server at the given target path. Used for REST endpoints that support
// multiple HTTP methods (GET, POST, DELETE).
func (s *Server) proxyPassThrough(w http.ResponseWriter, r *http.Request, targetPath string) {
	base := ""
	if s.managementBaseURL != nil {
		base = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		base = s.targets[0].URLs[0]
	}
	if base == "" {
		http.Error(w, "no management endpoint configured", http.StatusBadGateway)
		return
	}
	targetURL, err := buildTargetURL(base, targetPath, r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid management endpoint", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "failed to build proxy request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	// Forward the gateway service-account credential when configured.
	if s.gatewayBasicCred != "" {
		req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, "management API unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
// gatewayCall makes an authenticated HTTP request to a gateway management endpoint.
func (s *Server) gatewayCall(ctx context.Context, method, targetURL string, body []byte) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.gatewayBasicCred != "" {
		req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

// preSyncDeploy applies tenants and cache seeds from the bundle to one gateway target
// before the main /sync call. Errors are logged but non-fatal â€” /sync always runs.
func (s *Server) preSyncDeploy(ctx context.Context, targetBase string, payload []byte) {
	var bundle control.UnifiedSyncRequest
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return // unparseable â€” let /sync handle it
	}

	// 1. Tenants
	for _, t := range bundle.Tenants {
		if t.Action == "delete" && len(t.Aliases) > 0 {
			u, err := buildTargetURL(targetBase, "/tenants/"+t.Aliases[0], "")
			if err != nil {
				continue
			}
			if _, status, err := s.gatewayCall(ctx, http.MethodDelete, u, nil); err != nil || status >= 300 {
				log.Printf("[Studio] preSyncDeploy: delete tenant %q: status=%d err=%v", t.Aliases[0], status, err)
			}
		} else if t.Action != "delete" {
			body, _ := json.Marshal(t)
			u, err := buildTargetURL(targetBase, "/tenants", "")
			if err != nil {
				continue
			}
			if _, status, err := s.gatewayCall(ctx, http.MethodPost, u, body); err != nil || status >= 300 {
				log.Printf("[Studio] preSyncDeploy: upsert tenant %v: status=%d err=%v", t.Aliases, status, err)
			}
		}
	}

	// 2. Cache seeds
	for _, seed := range bundle.CacheSeeds {
		for _, alias := range seed.Tenants {
			if seed.Action == "delete" {
				u, err := buildTargetURL(targetBase, "/cache/"+alias+"/"+seed.Key, "")
				if err != nil {
					continue
				}
				if _, status, err := s.gatewayCall(ctx, http.MethodDelete, u, nil); err != nil || status >= 300 {
					log.Printf("[Studio] preSyncDeploy: delete cache %q/%q: status=%d err=%v", alias, seed.Key, status, err)
				}
			} else {
				body, _ := json.Marshal(map[string]any{"value": seed.Value, "ttl": seed.TTL})
				u, err := buildTargetURL(targetBase, "/cache/"+alias+"/"+seed.Key, "")
				if err != nil {
					continue
				}
				if _, status, err := s.gatewayCall(ctx, http.MethodPut, u, body); err != nil || status >= 300 {
					log.Printf("[Studio] preSyncDeploy: seed cache %q/%q: status=%d err=%v", alias, seed.Key, status, err)
				}
			}
		}
	}

	// 3. API keys
	for _, keyDef := range bundle.APIKeys {
		if keyDef.Action == "delete" {
			continue // key deletion not supported via bundle (use UI)
		}
		rawKey, err := resolveKeyRef(keyDef.KeyRef)
		if err != nil {
			log.Printf("[Studio] preSyncDeploy: api_key %q: resolve key_ref: %v", keyDef.Alias, err)
			continue
		}
		appID := s.findOrCreateApp(ctx, targetBase, keyDef.App)
		if appID == 0 {
			log.Printf("[Studio] preSyncDeploy: api_key %q: could not find/create app %q", keyDef.Alias, keyDef.App)
			continue
		}
		tenantIDs := s.resolveTenantIDs(ctx, targetBase, keyDef.AllowedTenants)
		importBody, _ := json.Marshal(map[string]any{
			"alias":           keyDef.Alias,
			"raw_key":         rawKey,
			"allowed_tenants": tenantIDs,
			"expires_at":      keyDef.ExpiresAt,
		})
		u, err := buildTargetURL(targetBase, fmt.Sprintf("/apps/%d/keys/import", appID), "")
		if err != nil {
			continue
		}
		if _, status, err := s.gatewayCall(ctx, http.MethodPost, u, importBody); err != nil || status >= 300 {
			log.Printf("[Studio] preSyncDeploy: import api_key %q: status=%d err=%v", keyDef.Alias, status, err)
		}
	}
}

// â”€â”€â”€ Release Management (S9) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// releasesHandler dispatches POST /api/releases (create) and GET /api/releases (list).
func (s *Server) releasesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createReleaseHandler(w, r)
	case http.MethodGet:
		s.listReleasesHandler(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// releaseByIDHandler handles GET /api/releases/:id.
// Sub-paths like /api/releases/:id/deploy are not handled here â€” they will be
// added in S10. Unknown sub-paths return 404.
func (s *Server) releaseByIDHandler(w http.ResponseWriter, r *http.Request) {
	// strip /api/releases/
	rest := strings.TrimPrefix(r.URL.Path, "/api/releases/")

	// Parse the path to detect sub-paths: /deploy, /diff/:other_id
	parts := strings.SplitN(rest, "/", 3)
	id := parts[0]
	if id == "" {
		// Trailing-slash redirect to list handler.
		s.releasesHandler(w, r)
		return
	}

	// Handle sub-paths
	if len(parts) > 1 {
		switch parts[1] {
		case "deploy":
			s.releaseDeployHandler(w, r, id)
			return
		case "approve":
			s.releaseApproveHandler(w, r, id)
			return
		case "diff":
			if len(parts) < 3 || parts[2] == "" {
				http.Error(w, "other release ID required", http.StatusBadRequest)
				return
			}
			s.releaseDiffHandler(w, r, id, parts[2])
			return
		case "merge":
			s.releaseMergeHandler(w, r)
			return
		case "cherry-pick":
			s.releaseCherryPickHandler(w, r)
			return
		case "branch":
			s.releaseBranchHandler(w, r)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}

	// Handle bare ID lookup (existing behavior)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rec, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

// releaseDiffHandler handles GET /api/releases/:id/diff/:other_id.
// Returns a diff of flow names and API paths between two releases.
func (s *Server) releaseDiffHandler(w http.ResponseWriter, r *http.Request, id string, otherID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get both releases
	rec1, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "first release not found", http.StatusNotFound)
		return
	}
	rec2, err := s.store.Get(r.Context(), otherID)
	if err != nil {
		http.Error(w, "second release not found", http.StatusNotFound)
		return
	}

	// Extract flow and API names from both releases
	flows1, apis1 := extractFlowsAndAPIs(rec1.Payload)
	flows2, apis2 := extractFlowsAndAPIs(rec2.Payload)

	// Compute diff: flows/APIs only in rec1, only in rec2, and in both
	resp := map[string]any{
		"flows_added":    setDiff(flows2, flows1),
		"flows_removed":  setDiff(flows1, flows2),
		"apis_added":     setDiff(apis2, apis1),
		"apis_removed":   setDiff(apis1, apis2),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// extractFlowsAndAPIs parses a release payload to extract flow names and API paths.
func extractFlowsAndAPIs(payload json.RawMessage) (map[string]struct{}, map[string]struct{}) {
	flows := make(map[string]struct{})
	apis := make(map[string]struct{})

	var bundle control.UnifiedSyncRequest
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return flows, apis
	}

	for _, f := range bundle.Flows {
		flows[f.Name] = struct{}{}
	}
	for _, a := range bundle.Apis {
		apiKey := a.Path
		if a.Method != "" {
			apiKey = a.Method + " " + a.Path
		}
		apis[apiKey] = struct{}{}
	}

	return flows, apis
}

// setDiff returns elements in a that are not in b.
func setDiff(a, b map[string]struct{}) []string {
	var result []string
	for k := range a {
		if _, exists := b[k]; !exists {
			result = append(result, k)
		}
	}
	sort.Strings(result)
	return result
}

// createReleaseHandler handles POST /api/releases.
// Accepts application/json (plain bundle or envelope), application/yaml, or
// multipart/form-data. Validates for forbidden _slot keys, translates named
// variable fields to their internal _slot equivalents, runs the linter, and
// stores the release. Supports ?dry_run=true to validate without storing.
func (s *Server) createReleaseHandler(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dry_run") == "true"

	bundle, meta, err := s.parseBundleRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate: reject _slot-suffixed keys in Input maps before any translation.
	if err := validateNoSlotKeys(bundle.Flows); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Run linter on the parsed bundle while user-facing keys are still intact
	// (e.g. system_var, hit_var). Translation happens below after lint passes.
	loadResult := rahsync.LoadResult{
		Bundle:    bundle,
		SourceMap: rahsync.SourceMap{},
		Issues:    []rahsync.LintIssue{},
	}
	lintIssues := rahsync.Lint(loadResult)

	var ls LintSummary
	var warnings []string
	for _, iss := range lintIssues {
		switch iss.Severity {
		case rahsync.SeverityError:
			ls.Errors++
		case rahsync.SeverityWarning:
			ls.Warnings++
			warnings = append(warnings, iss.Message)
		case rahsync.SeverityInfo:
			ls.Infos++
		}
	}

	resp := CreateReleaseResponse{
		LintSummary: ls,
		Warnings:    warnings,
		Issues:      lintIssues,
	}

	if dryRun {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Abort if there are lint errors (not warnings).
	if ls.Errors > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Translate user-facing variable keys (e.g. system_var â†’ system_slot) now
	// that lint has passed. The compiler expects the internal _slot keys.
	translateBundleVarKeys(bundle.Flows)

	// Serialize the translated bundle as the stored payload.
	payload, err := json.Marshal(bundle)
	if err != nil {
		http.Error(w, "failed to serialize bundle", http.StatusInternalServerError)
		return
	}

	// Compute SHA-256 bundle hash.
	sum := sha256.Sum256(payload)
	bundleHash := hex.EncodeToString(sum[:])

	rid := s.nextReleaseID()
	rec := ReleaseRecord{
		ReleaseID:   rid,
		CreatedAt:   nowJSON(),
		Payload:     json.RawMessage(payload),
		BundleHash:  bundleHash,
		Tag:         meta.Tag,
		GitCommit:   meta.GitCommit,
		GitBranch:   meta.GitBranch,
		GitRepo:     meta.GitRepo,
		SourcePath:  meta.SourcePath,
		Author:      meta.Author,
		LintSummary: ls,
	}
	if err := s.store.Put(r.Context(), rec); err != nil {
		http.Error(w, "failed to store release", http.StatusInternalServerError)
		return
	}

	resp.ReleaseID = rid
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// listReleasesHandler handles GET /api/releases with optional pagination and
// environment-status filtering.
//
// Query params:
//   - cursor     release ID to start after (exclusive)
//   - limit      page size, 1-100 (default 20)
//   - env        environment name to filter by (e.g. "uat")
//   - env_status status value within that environment (e.g. "deployed")
func (s *Server) listReleasesHandler(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.List(r.Context())
	if err != nil {
		http.Error(w, "failed to list releases", http.StatusInternalServerError)
		return
	}

	// Optional environment filter.
	envFilter := r.URL.Query().Get("env")
	statusFilter := r.URL.Query().Get("env_status")
	if envFilter != "" {
		filtered := all[:0]
		for _, rec := range all {
			if dep, ok := rec.Environments[envFilter]; ok {
				if statusFilter == "" || dep.Status == statusFilter {
					filtered = append(filtered, rec)
				}
			}
		}
		all = filtered
	}

	total := len(all)

	// Cursor-based pagination.
	limit := 20
	if n, err2 := strconv.Atoi(r.URL.Query().Get("limit")); err2 == nil && n > 0 {
		if n > 100 {
			n = 100
		}
		limit = n
	}

	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		startIdx := -1
		for i, rec := range all {
			if rec.ReleaseID == cursor {
				startIdx = i + 1
				break
			}
		}
		if startIdx < 0 || startIdx >= len(all) {
			all = nil
		} else {
			all = all[startIdx:]
		}
	}

	var nextCursor string
	if len(all) > limit {
		nextCursor = all[limit].ReleaseID
		all = all[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ReleaseListResponse{
		Releases:   all,
		NextCursor: nextCursor,
		Total:      total,
	})
}

// parseBundleRequest reads the request body and extracts the UnifiedSyncRequest
// bundle and metadata fields.
//
// Metadata precedence: X-* request headers > body envelope / form fields.
//
// Supported content types:
//   - application/json: plain UnifiedSyncRequest, or bundleWrapper envelope
//   - application/yaml / text/yaml: plain UnifiedSyncRequest in YAML
//   - multipart/form-data: "bundle" field (JSON or YAML), metadata as form values
func (s *Server) parseBundleRequest(r *http.Request) (control.UnifiedSyncRequest, releaseMeta, error) {
	var bundle control.UnifiedSyncRequest
	var meta releaseMeta

	// Headers take highest precedence.
	meta.Tag = r.Header.Get("X-Tag")
	meta.GitCommit = r.Header.Get("X-Git-Commit")
	meta.GitBranch = r.Header.Get("X-Git-Branch")
	meta.GitRepo = r.Header.Get("X-Git-Repo")
	meta.SourcePath = r.Header.Get("X-Source-Path")
	meta.Author = r.Header.Get("X-Author")

	ct := r.Header.Get("Content-Type")

	switch {
	case strings.Contains(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return bundle, meta, fmt.Errorf("failed to parse multipart form: %w", err)
		}
		var data []byte
		if bundleStr := r.FormValue("bundle"); bundleStr != "" {
			data = []byte(bundleStr)
		} else {
			f, _, err := r.FormFile("bundle")
			if err != nil {
				return bundle, meta, errors.New("multipart form missing 'bundle' field")
			}
			defer f.Close()
			raw, err := io.ReadAll(f)
			if err != nil {
				return bundle, meta, fmt.Errorf("failed to read bundle file: %w", err)
			}
			data = raw
		}
		var err error
		bundle, err = parseBundle(data)
		if err != nil {
			return bundle, meta, err
		}
		// Form fields fill in any metadata not supplied via headers.
		if meta.Tag == "" {
			meta.Tag = r.FormValue("tag")
		}
		if meta.GitCommit == "" {
			meta.GitCommit = r.FormValue("git_commit")
		}
		if meta.GitBranch == "" {
			meta.GitBranch = r.FormValue("git_branch")
		}
		if meta.GitRepo == "" {
			meta.GitRepo = r.FormValue("git_repo")
		}
		if meta.SourcePath == "" {
			meta.SourcePath = r.FormValue("source_path")
		}
		if meta.Author == "" {
			meta.Author = r.FormValue("author")
		}

	case strings.Contains(ct, "application/yaml") || strings.Contains(ct, "text/yaml"):
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return bundle, meta, fmt.Errorf("failed to read request body: %w", err)
		}
		bundle, err = parseBundleYAML(data)
		if err != nil {
			return bundle, meta, err
		}

	default: // application/json or unspecified â€” try envelope, then plain bundle
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return bundle, meta, fmt.Errorf("failed to read request body: %w", err)
		}
		var env bundleWrapper
		if jsonErr := json.Unmarshal(data, &env); jsonErr == nil && env.Bundle != nil {
			bundle = *env.Bundle
			if meta.Tag == "" {
				meta.Tag = env.Tag
			}
			if meta.GitCommit == "" {
				meta.GitCommit = env.GitCommit
			}
			if meta.GitBranch == "" {
				meta.GitBranch = env.GitBranch
			}
			if meta.GitRepo == "" {
				meta.GitRepo = env.GitRepo
			}
			if meta.SourcePath == "" {
				meta.SourcePath = env.SourcePath
			}
			if meta.Author == "" {
				meta.Author = env.Author
			}
		} else {
			if err := json.Unmarshal(data, &bundle); err != nil {
				return bundle, meta, fmt.Errorf("failed to parse JSON bundle: %w", err)
			}
		}
	}

	return bundle, meta, nil
}

// parseBundle tries JSON first, then YAML (via JSON intermediary) to decode
// raw bytes into a UnifiedSyncRequest.
func parseBundle(data []byte) (control.UnifiedSyncRequest, error) {
	var bundle control.UnifiedSyncRequest
	if err := json.Unmarshal(data, &bundle); err == nil {
		return bundle, nil
	}
	return parseBundleYAML(data)
}

// parseBundleYAML parses YAML bytes into a UnifiedSyncRequest by first
// converting YAML â†’ generic map (preserving snake_case keys) â†’ JSON â†’ struct.
// This is necessary because control structs have json: tags but not yaml: tags.
func parseBundleYAML(data []byte) (control.UnifiedSyncRequest, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return control.UnifiedSyncRequest{}, fmt.Errorf("failed to parse YAML bundle: %w", err)
	}
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return control.UnifiedSyncRequest{}, fmt.Errorf("failed to normalize YAML bundle: %w", err)
	}
	var bundle control.UnifiedSyncRequest
	if err := json.Unmarshal(jsonData, &bundle); err != nil {
		return bundle, fmt.Errorf("failed to decode bundle: %w", err)
	}
	return bundle, nil
}

// validateNoSlotKeys returns an error if any step in any flow contains a
// _slot-suffixed key in its Input map. Such keys are internal compiler details
// that must not appear in user-authored bundle files.
func validateNoSlotKeys(flows []control.FlowUpdate) error {
	for _, f := range flows {
		if err := validateStepsNoSlotKeys(f.Instructions, f.Name); err != nil {
			return err
		}
	}
	return nil
}

func validateStepsNoSlotKeys(steps []control.StepConfig, flowName string) error {
	for i, step := range steps {
		for k := range step.Input {
			if strings.HasSuffix(k, "_slot") {
				// Try to find a user-facing alternative.
				alt := rahsync.SlotKeyAlternative("input." + k)
				if alt == "" {
					alt = rahsync.SlotKeyAlternative(k)
				}
				msg := fmt.Sprintf("flow %q step %d: input key %q is an internal slot index â€” use named variable fields instead of internal slot indices", flowName, i+1, k)
				if alt != "" {
					msg += fmt.Sprintf(" (use %q instead)", strings.TrimPrefix(alt, "input."))
				}
				return errors.New(msg)
			}
		}
		// Recurse into inline sub-flows.
		if len(step.Do) > 0 {
			if err := validateStepsNoSlotKeys(step.Do, flowName); err != nil {
				return err
			}
		}
		for _, br := range step.Branches {
			if err := validateStepsNoSlotKeys(br.Flow, flowName); err != nil {
				return err
			}
		}
	}
	return nil
}

// translateBundleVarKeys rewrites user-facing variable field names in every
// StepConfig.Input map to their internal _slot equivalents expected by the
// compiler. For example, within the Input map, "url_var" â†’ "url_slot" for
// steps that pass variables through the input block (e.g. check_upstream_rate_limit,
// emit_event).
func translateBundleVarKeys(flows []control.FlowUpdate) {
	for i := range flows {
		translateStepVarKeys(flows[i].Instructions)
	}
}

func translateStepVarKeys(steps []control.StepConfig) {
	for i := range steps {
		if len(steps[i].Input) > 0 {
			newInput := make(map[string]string, len(steps[i].Input))
			for k, v := range steps[i].Input {
				// namedVarToSlotKey uses "input.KEY_var" as the lookup key.
				slotKey, ok := rahsync.NamedVarToSlotKey("input." + k)
				if ok {
					// Strip the "input." prefix to get the bare map key.
					newInput[strings.TrimPrefix(slotKey, "input.")] = v
				} else {
					newInput[k] = v
				}
			}
			steps[i].Input = newInput
		}
		// Recurse into inline sub-flows.
		if len(steps[i].Do) > 0 {
			translateStepVarKeys(steps[i].Do)
		}
		for j := range steps[i].Branches {
			translateStepVarKeys(steps[i].Branches[j].Flow)
		}
	}
}

func (s *Server) proxyToDefault(w http.ResponseWriter, r *http.Request, method, path string) {
	if r.Method != method {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := ""
	if s.managementBaseURL != nil {
		base = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		base = s.targets[0].URLs[0]
	}
	if base == "" {
		http.Error(w, "no default management endpoint", http.StatusBadGateway)
		return
	}
	targetURL, err := buildTargetURL(base, path, r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid management endpoint", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to build proxy request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	// Forward the gateway service-account credential when configured.
	if s.gatewayBasicCred != "" {
		req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, "Failed to call gateway management API", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
// auditLogHandler returns audit records newest-first.
// Accepts optional ?limit=N query param (default 200, max 1000).
func (s *Server) auditLogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
			if limit > 1000 {
				limit = 1000
			}
		}
	}
	records, err := s.auditStore.List(r.Context(), limit)
	if err != nil {
		http.Error(w, "failed to list audit records", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []AuditRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(records)
}

// statusRecorder captures the HTTP status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// recordingProxy proxies the request to targetPath and appends an audit record.
// Only called for mutating methods (POST, PUT, PATCH, DELETE).
func (s *Server) recordingProxy(w http.ResponseWriter, r *http.Request, targetPath, actor, resourceType, action string) {
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	s.proxyPassThrough(rec, r, targetPath)
	status := "success"
	if rec.status >= 400 {
		status = "failure"
	}
	// Extract resource ID from the last non-empty path segment.
	resourceID := ""
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) > 0 {
		resourceID = parts[len(parts)-1]
	}
	go s.auditStore.Append(context.Background(), AuditRecord{
		ID:           fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:    time.Now().UTC(),
		Actor:        actor,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Status:       status,
		Summary:      fmt.Sprintf("%s %s %s", actor, action, resourceID),
	})
}