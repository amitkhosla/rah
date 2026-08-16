package apikey

import (
	"sync"
	"sync/atomic"
	"time"
)

// AppType identifies the intended runtime model of an app.
type AppType string

const (
	AppTypeWeb          AppType = "web"
	AppTypeAPIService   AppType = "api-service"
	AppTypeEventProcess AppType = "event-processor"
	AppTypeWebhook      AppType = "webhook"
)

// TenantMode identifies whether an app is tenant_aware or tenant_agnostic.
type TenantMode string

const (
	TenantModeAware    TenantMode = "tenant_aware"
	TenantModeAgnostic TenantMode = "tenant_agnostic"
)

// AppFlowBindings maps lifecycle flows to named flow definitions.
type AppFlowBindings struct {
	LoginFlow    string `json:"login_flow,omitempty"    yaml:"login_flow,omitempty"`
	LogoutFlow   string `json:"logout_flow,omitempty"   yaml:"logout_flow,omitempty"`
	CallbackFlow string `json:"callback_flow,omitempty" yaml:"callback_flow,omitempty"`
	RefreshFlow  string `json:"refresh_flow,omitempty"  yaml:"refresh_flow,omitempty"`
	ErrorFlow    string `json:"error_flow,omitempty"    yaml:"error_flow,omitempty"`
}

// AppEventBinding maps an event publisher+topic pair to a flow.
type AppEventBinding struct {
	Publisher string `json:"publisher" yaml:"publisher"`
	Topic     string `json:"topic"     yaml:"topic"`
	FlowName  string `json:"flow_name" yaml:"flow_name"`
}

// App is a stable consumer identity. Rate limiting is keyed by AppID so it
// survives key rotation.
type App struct {
	AppID          uint32            `json:"app_id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Labels         map[string]string `json:"labels,omitempty"`
	CreatedAt      int64             `json:"created_at"`
	UpdatedAt      int64             `json:"updated_at"`
	Type           AppType           `json:"type,omitempty"            yaml:"type,omitempty"`
	TenantMode     TenantMode        `json:"tenant_mode,omitempty"     yaml:"tenant_mode,omitempty"`
	FlowBindings   *AppFlowBindings  `json:"flow_bindings,omitempty"   yaml:"flow_bindings,omitempty"`
	EventBindings  []AppEventBinding `json:"event_bindings,omitempty"  yaml:"event_bindings,omitempty"`
	ReleaseChannel string            `json:"release_channel,omitempty" yaml:"release_channel,omitempty"`
	Version        string            `json:"version,omitempty"         yaml:"version,omitempty"`
}

var (
	globalAppIDCounter atomic.Uint32
	globalAppsByID     sync.Map // uint32 → App
)

// NextAppID returns the next monotonically increasing App ID.
func NextAppID() uint32 {
	return globalAppIDCounter.Add(1)
}

// BumpAppIDCounterIfNeeded ensures the counter is at least id so that future
// calls to NextAppID do not collide with restored records.
func BumpAppIDCounterIfNeeded(id uint32) {
	for {
		cur := globalAppIDCounter.Load()
		if id <= cur {
			return
		}
		if globalAppIDCounter.CompareAndSwap(cur, id) {
			return
		}
	}
}

// UpsertApp stores or replaces an App record. UpdatedAt is always set to now.
func UpsertApp(a App) {
	a.UpdatedAt = time.Now().Unix()
	globalAppsByID.Store(a.AppID, a)
}

// GetApp returns the App with the given ID, or nil if not found.
func GetApp(appID uint32) *App {
	v, ok := globalAppsByID.Load(appID)
	if !ok {
		return nil
	}
	a := v.(App)
	return &a
}

// DeleteApp removes an App by ID. The caller is responsible for also deleting
// (or disabling) all keys that belong to this app.
func DeleteApp(appID uint32) {
	globalAppsByID.Delete(appID)
}

// ListApps returns all Apps in arbitrary order.
func ListApps() []App {
	var out []App
	globalAppsByID.Range(func(_, v any) bool {
		out = append(out, v.(App))
		return true
	})
	return out
}
