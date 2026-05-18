package engine

/*
ApiDefinition represents one Stage-1 routed API group.

SubArena:
- Shared radix tree across all methods.

MethodRoots:
- Entry index for each method.
*/

// AsyncMode controls per-endpoint async behaviour, set at compile time from ApiConfig.Async.
type AsyncMode uint8

const (
	AsyncDisabled AsyncMode = 0 // always synchronous (default)
	AsyncAllowed  AsyncMode = 1 // client opts in via Prefer/X-Async header or ?async=true
	AsyncForced   AsyncMode = 2 // always async regardless of client preference
)

// RateLimitMode controls how the rate limit counter key is scoped for an endpoint.
// The zero value (RateLimitModeTenant) is the default and preserves existing behaviour.
type RateLimitMode uint8

const (
	RateLimitModeTenant RateLimitMode = 0 // counter key includes TenantID (default)
	RateLimitModeGlobal RateLimitMode = 1 // counter key is tenant-agnostic (shared across all tenants)
)

type Endpoint struct {
	EndpointId          uint8         // sequential within ApiDefinition (0–255)
	AsyncMode           AsyncMode     // set at bake time from ApiConfig.Async
	RateLimitMode       RateLimitMode // 0=tenant (default), 1=global (all tenants share counter)
	APIRateLimitId      uint16        // rate limit config at API level; 0 = gateway default
	EndpointRateLimitId uint16        // rate limit config at endpoint level; 0 = inherit API
	Plan                []Instruction
}

type ApiDefinition struct {
	Id          uint32
	BaseRawPath string
	AliasPaths  []string // additional basepaths that also route to this API

	MethodRoots [5]uint32

	SubArena  []SubRouteNode
	Endpoints []Endpoint
}

/*
MethodStringToIdx converts string method during compile-time.
*/
func MethodStringToIdx(m string) int {
	switch m {
	case "GET":
		return 0
	case "POST":
		return 1
	case "PUT":
		return 2
	case "DELETE":
		return 3
	default:
		return 4
	}
}

/*
MethodToIdx converts runtime method []byte to index.
*/
func MethodToIdx(m []byte) int {
	if len(m) == 0 {
		return 4
	}

	switch m[0] {
	case 'G':
		return 0
	case 'P':
		if len(m) > 1 && m[1] == 'O' {
			return 1
		}
		if len(m) > 1 && m[1] == 'U' {
			return 2
		}
		return 4
	case 'D':
		return 3
	default:
		return 4
	}
}

/*
BakeDefinition initializes a new ApiDefinition.

Design Notes:
- Each API Definition owns a shared SubArena tree.
- MethodRoots default to 0 (root node).
- SubArena is lazily initialized during BakeSubRouter.
*/
func BakeDefinition(id uint32, path string) *ApiDefinition {
	return &ApiDefinition{
		Id:          id,
		BaseRawPath: path,
		MethodRoots: [5]uint32{0, 0, 0, 0, 0},
		SubArena:    make([]SubRouteNode, 0, 8),
		Endpoints:   make([]Endpoint, 0, 4),
	}
}
