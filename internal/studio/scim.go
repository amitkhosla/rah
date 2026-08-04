package studio

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SCIMConfig configures SCIM 2.0 provisioning from an enterprise IDP.
type SCIMConfig struct {
	Enabled           bool            `json:"enabled"             yaml:"enabled"`
	Token             string          `json:"token"               yaml:"token"` // Bearer token IDP sends
	GroupRoleMappings []SCIMGroupRole `json:"group_role_mappings" yaml:"group_role_mappings"`
}

// SCIMGroupRole maps a SCIM group display name to a rah role.
type SCIMGroupRole struct {
	GroupDisplayName string   `json:"group_display_name" yaml:"group_display_name"`
	Role             string   `json:"role"               yaml:"role"`
	AllowedEnvs      []string `json:"allowed_envs,omitempty" yaml:"allowed_envs,omitempty"`
}

// SCIMUser is the SCIM 2.0 User resource representation.
type SCIMUser struct {
	Schemas  []string       `json:"schemas"`
	ID       string         `json:"id"`
	UserName string         `json:"userName"`
	Name     *SCIMName      `json:"name,omitempty"`
	Emails   []SCIMEmail    `json:"emails,omitempty"`
	Active   bool           `json:"active"`
	Groups   []SCIMGroupRef `json:"groups,omitempty"`
	Meta     SCIMMeta       `json:"meta"`
}

// SCIMName holds formatted name components.
type SCIMName struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// SCIMEmail is one email address entry on a SCIM user.
type SCIMEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
}

// SCIMGroupRef is a reference to a group the user belongs to.
type SCIMGroupRef struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

// SCIMMeta holds SCIM resource metadata.
type SCIMMeta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location,omitempty"`
}

// SCIMListResponse is the SCIM list response envelope.
type SCIMListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []any    `json:"Resources"`
}

// SCIMGroup is the SCIM 2.0 Group resource.
type SCIMGroup struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Members     []SCIMGroupMember `json:"members,omitempty"`
	Meta        SCIMMeta          `json:"meta"`
}

// SCIMGroupMember is a reference to a user within a SCIM group.
type SCIMGroupMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

// scimError writes a SCIM-compliant error response.
func scimError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
		"detail":  detail,
		"status":  status,
	})
}

// scimTokenMiddleware validates the SCIM bearer token using constant-time comparison.
func (s *Server) scimTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.SCIM == nil || !s.config.SCIM.Enabled {
			scimError(w, http.StatusServiceUnavailable, "SCIM provisioning not enabled")
			return
		}
		if s.config.SCIM.Token == "" {
			scimError(w, http.StatusServiceUnavailable, "SCIM token not configured")
			return
		}
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth || subtle.ConstantTimeCompare([]byte(token), []byte(s.config.SCIM.Token)) != 1 {
			scimError(w, http.StatusUnauthorized, "invalid or missing SCIM token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// scimResolveRole maps SCIM group names to a rah role. First match wins.
// Returns "viewer" with nil envs if no group matches.
func scimResolveRole(groups []string, mappings []SCIMGroupRole) (role string, allowedEnvs []string) {
	for _, g := range groups {
		for _, m := range mappings {
			if strings.EqualFold(g, m.GroupDisplayName) {
				return m.Role, m.AllowedEnvs
			}
		}
	}
	return "viewer", nil
}

// studioUserToSCIM converts a StudioUser to its SCIM representation.
func studioUserToSCIM(u StudioUser, location string) SCIMUser {
	emails := []SCIMEmail{}
	if u.SSOEmail != "" {
		emails = append(emails, SCIMEmail{Value: u.SSOEmail, Primary: true})
	}
	groups := []SCIMGroupRef{}
	for _, g := range u.SCIMGroups {
		groups = append(groups, SCIMGroupRef{Value: g, Display: g})
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return SCIMUser{
		Schemas:  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		ID:       u.SCIMId,
		UserName: u.SSOEmail,
		Emails:   emails,
		Active:   !u.SCIMDeprovisioned,
		Groups:   groups,
		Meta: SCIMMeta{
			ResourceType: "User",
			Created:      now,
			LastModified: now,
			Location:     location,
		},
	}
}

// ── SCIM Handler Implementations ─────────────────────────────────────────────

// scimUsersHandler handles GET (list all users) and POST (create/provision user).
func (s *Server) scimUsersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/scim+json")

	switch r.Method {
	case http.MethodGet:
		s.scimGetUsers(w, r)
	case http.MethodPost:
		s.scimCreateUser(w, r)
	default:
		scimError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed", r.Method))
	}
}

// scimGetUsers lists all users from the store, converted to SCIM format.
func (s *Server) scimGetUsers(w http.ResponseWriter, r *http.Request) {
	users := s.userStore.List()
	resources := make([]any, len(users))
	for i, u := range users {
		location := buildSCIMLocation(r, "/scim/v2/Users/"+u.SCIMId)
		resources[i] = studioUserToSCIM(u, location)
	}

	resp := SCIMListResponse{
		Schemas:      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		TotalResults: len(users),
		StartIndex:   1,
		ItemsPerPage: len(users),
		Resources:    resources,
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimCreateUser provisions a new user from SCIM request body.
func (s *Server) scimCreateUser(w http.ResponseWriter, r *http.Request) {
	var req SCIMUser
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		scimError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.UserName == "" {
		scimError(w, http.StatusBadRequest, "userName is required")
		return
	}

	// Extract email from emails array (use first or primary)
	var email string
	if len(req.Emails) > 0 {
		for _, e := range req.Emails {
			if e.Primary {
				email = e.Value
				break
			}
		}
		if email == "" {
			email = req.Emails[0].Value
		}
	}

	// Resolve role from groups
	groupDisplayNames := make([]string, len(req.Groups))
	for i, g := range req.Groups {
		groupDisplayNames[i] = g.Display
	}
	role, allowedEnvs := scimResolveRole(groupDisplayNames, s.config.SCIM.GroupRoleMappings)

	// Generate SCIMId if not provided
	scimID := req.ID
	if scimID == "" {
		scimID = strings.ToLower(req.UserName)
	}

	user := StudioUser{
		Username:          req.UserName,
		SSOEmail:          email,
		Role:              role,
		AllowedEnvs:       allowedEnvs,
		SCIMId:            scimID,
		SCIMGroups:        groupDisplayNames,
		SSOProvisioned:    true,
		SSOProvider:       "SCIM",
		SCIMDeprovisioned: false,
	}

	if err := s.userStore.UpsertSSO(user); err != nil {
		scimError(w, http.StatusInternalServerError, fmt.Sprintf("failed to provision user: %v", err))
		return
	}

	// Log audit event
	_ = s.auditStore.Append(r.Context(), AuditRecord{
		ID:           fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		Timestamp:    time.Now(),
		Actor:        "SCIM",
		Action:       "provision_user",
		ResourceType: "user",
		ResourceID:   user.Username,
		Status:       "success",
		Summary:      fmt.Sprintf("User %s provisioned via SCIM", user.Username),
	})

	location := buildSCIMLocation(r, "/scim/v2/Users/"+scimID)
	resp := studioUserToSCIM(user, location)
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Location", location)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimUserByIDHandler handles GET, PUT, PATCH, DELETE for /scim/v2/Users/{id}.
func (s *Server) scimUserByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/scim+json")

	// Extract ID from path
	id := strings.TrimPrefix(r.URL.Path, "/scim/v2/Users/")

	switch r.Method {
	case http.MethodGet:
		s.scimGetUser(w, r, id)
	case http.MethodPut:
		s.scimUpdateUser(w, r, id)
	case http.MethodPatch:
		s.scimPatchUser(w, r, id)
	case http.MethodDelete:
		s.scimDeleteUser(w, r, id)
	default:
		scimError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed", r.Method))
	}
}

// scimGetUser retrieves a single user by SCIM ID.
func (s *Server) scimGetUser(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := s.userStore.GetBySCIMId(id)
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}

	location := buildSCIMLocation(r, "/scim/v2/Users/"+id)
	resp := studioUserToSCIM(*user, location)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimUpdateUser replaces a user (PUT).
func (s *Server) scimUpdateUser(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := s.userStore.GetBySCIMId(id)
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}

	var req SCIMUser
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		scimError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.UserName == "" {
		scimError(w, http.StatusBadRequest, "userName is required")
		return
	}

	// Extract email
	var email string
	if len(req.Emails) > 0 {
		for _, e := range req.Emails {
			if e.Primary {
				email = e.Value
				break
			}
		}
		if email == "" {
			email = req.Emails[0].Value
		}
	}

	// Resolve role from groups
	groupDisplayNames := make([]string, len(req.Groups))
	for i, g := range req.Groups {
		groupDisplayNames[i] = g.Display
	}
	role, allowedEnvs := scimResolveRole(groupDisplayNames, s.config.SCIM.GroupRoleMappings)

	// Update user
	user.Username = req.UserName
	user.SSOEmail = email
	user.Role = role
	user.AllowedEnvs = allowedEnvs
	user.SCIMGroups = groupDisplayNames
	user.SCIMDeprovisioned = !req.Active

	if err := s.userStore.UpsertSSO(*user); err != nil {
		scimError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update user: %v", err))
		return
	}

	// Log audit event
	_ = s.auditStore.Append(r.Context(), AuditRecord{
		ID:           fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		Timestamp:    time.Now(),
		Actor:        "SCIM",
		Action:       "update_user",
		ResourceType: "user",
		ResourceID:   user.Username,
		Status:       "success",
		Summary:      fmt.Sprintf("User %s updated via SCIM", user.Username),
	})

	location := buildSCIMLocation(r, "/scim/v2/Users/"+id)
	resp := studioUserToSCIM(*user, location)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimPatchUser handles PATCH operations (e.g., activate/deactivate).
func (s *Server) scimPatchUser(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := s.userStore.GetBySCIMId(id)
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}

	var patchReq struct {
		Operations []struct {
			Op    string      `json:"op"`
			Path  string      `json:"path"`
			Value interface{} `json:"value"`
		} `json:"Operations"`
	}

	if err := json.NewDecoder(r.Body).Decode(&patchReq); err != nil {
		scimError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	// Process patch operations
	for _, op := range patchReq.Operations {
		if strings.EqualFold(op.Op, "Replace") && strings.EqualFold(op.Path, "active") {
			active, ok := op.Value.(bool)
			if !ok {
				scimError(w, http.StatusBadRequest, "active field must be boolean")
				return
			}

			if !active {
				// Deprovisioning: mark as deprovisioned and delete sessions
				user.SCIMDeprovisioned = true
				_ = s.userStore.UpsertSSO(*user)
				s.sessions.deleteByUsername(user.Username)

				// Log audit event
				_ = s.auditStore.Append(r.Context(), AuditRecord{
					ID:           fmt.Sprintf("audit-%d", time.Now().UnixNano()),
					Timestamp:    time.Now(),
					Actor:        "SCIM",
					Action:       "deprovisioned_user",
					ResourceType: "user",
					ResourceID:   user.Username,
					Status:       "success",
					Summary:      fmt.Sprintf("User %s deprovisioned via SCIM", user.Username),
				})
			} else {
				// Reactivate
				user.SCIMDeprovisioned = false
				_ = s.userStore.UpsertSSO(*user)

				// Log audit event
				_ = s.auditStore.Append(r.Context(), AuditRecord{
					ID:           fmt.Sprintf("audit-%d", time.Now().UnixNano()),
					Timestamp:    time.Now(),
					Actor:        "SCIM",
					Action:       "reactivated_user",
					ResourceType: "user",
					ResourceID:   user.Username,
					Status:       "success",
					Summary:      fmt.Sprintf("User %s reactivated via SCIM", user.Username),
				})
			}
		}
	}

	location := buildSCIMLocation(r, "/scim/v2/Users/"+id)
	resp := studioUserToSCIM(*user, location)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimDeleteUser soft-deletes a user (marks deprovisioned and removes sessions).
func (s *Server) scimDeleteUser(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := s.userStore.GetBySCIMId(id)
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}

	// Soft-delete: mark as deprovisioned and remove sessions
	user.SCIMDeprovisioned = true
	if err := s.userStore.UpsertSSO(*user); err != nil {
		scimError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete user: %v", err))
		return
	}

	s.sessions.deleteByUsername(user.Username)

	// Log audit event
	_ = s.auditStore.Append(r.Context(), AuditRecord{
		ID:           fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		Timestamp:    time.Now(),
		Actor:        "SCIM",
		Action:       "delete_user",
		ResourceType: "user",
		ResourceID:   user.Username,
		Status:       "success",
		Summary:      fmt.Sprintf("User %s deleted via SCIM", user.Username),
	})

	w.WriteHeader(http.StatusNoContent)
}

// scimGroupsHandler handles GET only (we don't manage groups, IDPs push them to users).
func (s *Server) scimGroupsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/scim+json")

	if r.Method != http.MethodGet {
		scimError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed", r.Method))
		return
	}

	// Collect unique groups from all users
	groupMap := make(map[string]bool)
	users := s.userStore.List()
	for _, u := range users {
		for _, g := range u.SCIMGroups {
			groupMap[g] = true
		}
	}

	// Convert to sorted list for consistent ordering
	groups := make([]string, 0, len(groupMap))
	for g := range groupMap {
		groups = append(groups, g)
	}
	// Sort for deterministic output
	sort.Strings(groups)

	resources := make([]any, len(groups))
	for i, g := range groups {
		location := buildSCIMLocation(r, "/scim/v2/Groups/"+url.QueryEscape(g))
		members := s.groupMembers(g)
		resources[i] = SCIMGroup{
			Schemas:     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
			ID:          g,
			DisplayName: g,
			Members:     members,
			Meta: SCIMMeta{
				ResourceType: "Group",
				Location:     location,
			},
		}
	}

	resp := SCIMListResponse{
		Schemas:      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		TotalResults: len(groups),
		StartIndex:   1,
		ItemsPerPage: len(groups),
		Resources:    resources,
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// scimGroupByIDHandler handles GET only for a single group.
func (s *Server) scimGroupByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/scim+json")

	if r.Method != http.MethodGet {
		scimError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed", r.Method))
		return
	}

	// Extract group ID from path (URL-encoded group display name)
	id := strings.TrimPrefix(r.URL.Path, "/scim/v2/Groups/")
	groupName, err := url.QueryUnescape(id)
	if err != nil {
		scimError(w, http.StatusBadRequest, fmt.Sprintf("invalid group ID: %v", err))
		return
	}

	// Find all users with this group
	users := s.userStore.List()
	found := false
	members := make([]SCIMGroupMember, 0)

	for _, u := range users {
		for _, g := range u.SCIMGroups {
			if g == groupName {
				found = true
				members = append(members, SCIMGroupMember{
					Value:   u.SCIMId,
					Display: u.Username,
				})
				break
			}
		}
	}

	if !found && len(members) == 0 {
		scimError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", groupName))
		return
	}

	location := buildSCIMLocation(r, "/scim/v2/Groups/"+url.QueryEscape(groupName))
	resp := SCIMGroup{
		Schemas:     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		ID:          groupName,
		DisplayName: groupName,
		Members:     members,
		Meta: SCIMMeta{
			ResourceType: "Group",
			Location:     location,
		},
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// groupMembers collects all users in a given group.
func (s *Server) groupMembers(groupName string) []SCIMGroupMember {
	members := make([]SCIMGroupMember, 0)
	users := s.userStore.List()
	for _, u := range users {
		for _, g := range u.SCIMGroups {
			if g == groupName {
				members = append(members, SCIMGroupMember{
					Value:   u.SCIMId,
					Display: u.Username,
				})
				break
			}
		}
	}
	return members
}

// buildSCIMLocation constructs the location URL for a SCIM resource.
func buildSCIMLocation(r *http.Request, path string) string {
	if r.URL.Scheme != "" {
		return r.URL.Scheme + "://" + r.Host + path
	}
	return path
}
