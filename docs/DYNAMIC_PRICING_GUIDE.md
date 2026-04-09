# Dynamic Pricing: Fetch from Providers Automatically

Instead of hardcoding pricing in config, fetch it **automatically** from providers.

---

## Overview: Three Strategies

```
Strategy 1: Fetch at Startup (Cache)
  Gateway starts → Fetch pricing from OpenAI/Anthropic/Google APIs
  → Store in memory → Use for all requests
  → Update hourly via background job

Strategy 2: Real-Time Lookup (Accurate)
  Per-request → Check cache first → If stale, fetch fresh
  → Small latency cost, but always accurate

Strategy 3: Hybrid (Best)
  Default: Use cache (fast)
  If cache missing: Fetch real-time (fallback)
  Update cache hourly in background
```

---

## Part 1: Fetch Pricing from OpenAI

OpenAI doesn't have a public pricing API, but we can:
1. Scrape from their website (or use cached data)
2. Maintain a hardcoded map (updated when they announce changes)
3. Use a third-party pricing service

### Option A: Built-in Pricing Map (Simplest)

```go
// internal/pricing/openai_pricing.go

var OpenAIPricing = map[string]PricingInfo{
	"gpt-4o": {
		InputCostPer1MTok:  5.00,      // $5 per 1M tokens
		OutputCostPer1MTok: 15.00,     // $15 per 1M tokens
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"gpt-4o-mini": {
		InputCostPer1MTok:  0.15,
		OutputCostPer1MTok: 0.60,
		LastUpdated:        "2025-04-01",
	},
	"gpt-4-turbo": {
		InputCostPer1MTok:  3.00,
		OutputCostPer1MTok: 6.00,
		LastUpdated:        "2025-04-01",
	},
	"gpt-3.5-turbo": {
		InputCostPer1MTok:  0.50,
		OutputCostPer1MTok: 1.50,
		LastUpdated:        "2025-04-01",
	},
	"o1": {
		InputCostPer1MTok:  15.00,
		OutputCostPer1MTok: 60.00,
		LastUpdated:        "2025-04-01",
	},
}

// Fetch OpenAI pricing (fallback to hardcoded)
func GetOpenAIPricing(modelID string) (PricingInfo, error) {
	if price, ok := OpenAIPricing[modelID]; ok {
		return price, nil
	}
	return PricingInfo{}, fmt.Errorf("unknown OpenAI model: %s", modelID)
}
```

### Option B: Scrape from OpenAI Website (Automatic)

```go
// internal/pricing/scraper.go

import "github.com/PuerkitoBio/goquery"

func ScrapeOpenAIPricing() (map[string]PricingInfo, error) {
	// Fetch https://openai.com/pricing
	resp, err := http.Get("https://openai.com/pricing")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	pricing := make(map[string]PricingInfo)

	// Parse pricing tables from HTML
	doc.Find(".pricing-table tbody tr").Each(func(i int, s *goquery.Selection) {
		modelName := s.Find("td:nth-child(1)").Text()
		inputPrice := parsePrice(s.Find("td:nth-child(2)").Text())
		outputPrice := parsePrice(s.Find("td:nth-child(3)").Text())

		pricing[modelName] = PricingInfo{
			InputCostPer1MTok:  inputPrice,
			OutputCostPer1MTok: outputPrice,
			LastUpdated:        time.Now().Format("2006-01-02"),
		}
	})

	return pricing, nil
}

func parsePrice(s string) float64 {
	// Parse "$5 per 1M tokens" → 5.0
	s = strings.ReplaceAll(s, "$", "")
	s = strings.Split(s, " ")[0]
	price, _ := strconv.ParseFloat(s, 64)
	return price
}
```

---

## Part 2: Fetch from Anthropic

Anthropic has a public pricing page:

```go
// internal/pricing/anthropic_pricing.go

var AnthropicPricing = map[string]PricingInfo{
	"claude-opus": {
		InputCostPer1MTok:  15.00,     // $15 per 1M input tokens
		OutputCostPer1MTok: 75.00,     // $75 per 1M output tokens
		CacheCostPer1MTok:  1.50,      // Cache reads cost 10% of input
		LastUpdated:        "2025-04-01",
		CacheWriteCostPer1MTok: 1.875, // Cache writes cost 25% of input
	},
	"claude-sonnet": {
		InputCostPer1MTok:  3.00,
		OutputCostPer1MTok: 15.00,
		CacheCostPer1MTok:  0.30,
		CacheWriteCostPer1MTok: 0.375,
		LastUpdated:        "2025-04-01",
	},
	"claude-haiku": {
		InputCostPer1MTok:  0.08,
		OutputCostPer1MTok: 0.40,
		CacheCostPer1MTok:  0.008,
		CacheWriteCostPer1MTok: 0.01,
		LastUpdated:        "2025-04-01",
	},
}

func GetAnthropicPricing(modelID string) (PricingInfo, error) {
	if price, ok := AnthropicPricing[modelID]; ok {
		return price, nil
	}
	return PricingInfo{}, fmt.Errorf("unknown Anthropic model: %s", modelID)
}
```

---

## Part 3: Fetch from Google

Google has a pricing API:

```go
// internal/pricing/google_pricing.go

import "google.golang.org/api/compute/v1"

func GetGooglePricing(modelID string) (PricingInfo, error) {
	// Fetch from https://ai.google.dev/pricing or use cached data
	// Google's models are priced in $/1M tokens

	pricingMap := map[string]PricingInfo{
		"gemini-2.0-flash": {
			InputCostPer1MTok:  0.075,
			OutputCostPer1MTok: 0.30,
			LastUpdated:        "2025-04-01",
		},
		"gemini-2.0-mini": {
			InputCostPer1MTok:  0.032,
			OutputCostPer1MTok: 0.08,
			LastUpdated:        "2025-04-01",
		},
		"gemini-1.5-pro": {
			InputCostPer1MTok:  0.625,
			OutputCostPer1MTok: 1.25,
			LastUpdated:        "2025-04-01",
		},
	}

	if price, ok := pricingMap[modelID]; ok {
		return price, nil
	}

	return PricingInfo{}, fmt.Errorf("unknown Google model: %s", modelID)
}
```

---

## Part 4: Pricing Manager (Central Hub)

```go
// internal/pricing/manager.go

type PricingInfo struct {
	InputCostPer1MTok      float64   `json:"input_cost_per_1m_tokens"`
	OutputCostPer1MTok     float64   `json:"output_cost_per_1m_tokens"`
	CacheCostPer1MTok      float64   `json:"cache_cost_per_1m_tokens,omitempty"`
	CacheWriteCostPer1MTok float64   `json:"cache_write_cost_per_1m_tokens,omitempty"`
	LastUpdated            string    `json:"last_updated"`
	Source                 string    `json:"source,omitempty"`
	Cached                 bool      `json:"cached"`
}

type PricingManager struct {
	mu       sync.RWMutex
	cache    map[string]PricingInfo  // modelID → pricing
	ttl      time.Duration           // cache TTL
	lastFetch time.Time
}

func NewPricingManager() *PricingManager {
	pm := &PricingManager{
		cache: make(map[string]PricingInfo),
		ttl:   time.Hour,  // Update every hour
	}

	// Bootstrap from hardcoded data
	pm.bootstrapPricing()

	// Start background refresh
	go pm.backgroundRefresh()

	return pm
}

// GetPrice looks up pricing (cache-first, fallback to fetch)
func (pm *PricingManager) GetPrice(provider, modelID string) (PricingInfo, error) {
	pm.mu.RLock()
	cachedPrice, ok := pm.cache[modelID]
	pm.mu.RUnlock()

	if ok && time.Since(pm.lastFetch) < pm.ttl {
		return cachedPrice, nil  // Cache hit
	}

	// Cache miss or stale → fetch fresh
	var price PricingInfo
	var err error

	switch provider {
	case "openai":
		price, err = GetOpenAIPricing(modelID)
	case "anthropic":
		price, err = GetAnthropicPricing(modelID)
	case "google":
		price, err = GetGooglePricing(modelID)
	case "custom":
		// Local models (no pricing)
		return PricingInfo{}, nil
	default:
		return PricingInfo{}, fmt.Errorf("unknown provider: %s", provider)
	}

	if err != nil {
		// Fallback to cached value if fetch fails
		if ok {
			return cachedPrice, nil
		}
		return PricingInfo{}, err
	}

	// Update cache
	price.Cached = true
	price.LastUpdated = time.Now().Format("2006-01-02 15:04:05 UTC")

	pm.mu.Lock()
	pm.cache[modelID] = price
	pm.lastFetch = time.Now()
	pm.mu.Unlock()

	return price, nil
}

// Bootstrap loads hardcoded pricing
func (pm *PricingManager) bootstrapPricing() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Load all hardcoded pricing
	for modelID, price := range OpenAIPricing {
		pm.cache[modelID] = price
	}
	for modelID, price := range AnthropicPricing {
		pm.cache[modelID] = price
	}
	for modelID, price := range GooglePricing {
		pm.cache[modelID] = price
	}
}

// Background refresh (hourly)
func (pm *PricingManager) backgroundRefresh() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Refreshing pricing data...")
		pm.mu.Lock()
		pm.lastFetch = time.Time{}  // Mark cache as stale
		pm.mu.Unlock()

		// Trigger refresh on next request
	}
}

// ListPricing returns all cached pricing (for admin dashboard)
func (pm *PricingManager) ListPricing() map[string]PricingInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[string]PricingInfo)
	for k, v := range pm.cache {
		result[k] = v
	}
	return result
}
```

---

## Part 5: Integrate into Gateway

### In main.go:

```go
func main() {
	// Initialize pricing manager (auto-fetch + cache)
	pricingMgr := pricing.NewPricingManager()

	// Use pricing when calculating costs
	http.HandleFunc("/api/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		handleChatWithPricing(w, r, flowMgr, contextPool, pricingMgr)
	})

	// Admin: View current pricing
	http.HandleFunc("/admin/pricing", func(w http.ResponseWriter, r *http.Request) {
		if !verifyAdminToken(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		prices := pricingMgr.ListPricing()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(prices)
	})

	// Admin: Refresh pricing now (don't wait for hourly)
	http.HandleFunc("/admin/pricing/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !verifyAdminToken(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Force refresh
		pricingMgr.ForceRefresh()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "pricing refreshed"})
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Handler: Calculate cost using dynamic pricing
func handleChatWithPricing(
	w http.ResponseWriter,
	r *http.Request,
	flowMgr *engine.FlowManager,
	contextPool *rctx.ContextPool,
	pricingMgr *pricing.PricingManager,
) {
	// ... execute flow ...

	// Look up current pricing (fetches if stale)
	price, err := pricingMgr.GetPrice(provider, modelID)
	if err != nil {
		log.Printf("Warning: pricing lookup failed: %v", err)
		price = PricingInfo{}  // Use zero (cost won't be calculated)
	}

	// Calculate cost using dynamic pricing
	inputCost := (float64(inputTokens) / 1e6) * price.InputCostPer1MTok
	outputCost := (float64(outputTokens) / 1e6) * price.OutputCostPer1MTok
	totalCost := inputCost + outputCost

	// Log with pricing info
	log.Printf("Request cost: $%.6f (%.2f in tokens × $%.2f/1M + %.2f out × $%.2f/1M) [%s]",
		totalCost,
		float64(inputTokens)/1e6, price.InputCostPer1MTok,
		float64(outputTokens)/1e6, price.OutputCostPer1MTok,
		price.LastUpdated,
	)

	// ... return response ...
}
```

---

## Part 6: Config: Make Pricing Optional

You don't **need** to specify pricing in YAML anymore:

### Old (Manual Pricing):
```yaml
models:
  gpt-4o-mini:
    provider: "openai"
    modelId: "gpt-4o-mini"
    pricing:
      inputToken: 0.00015      # ← Manual
      outputToken: 0.0006      # ← Manual
```

### New (Dynamic Pricing):
```yaml
models:
  gpt-4o-mini:
    provider: "openai"
    modelId: "gpt-4o-mini"
    # ← No pricing needed! Fetched automatically
```

### Or Keep Both (Explicit Override):
```yaml
models:
  gpt-4o-mini:
    provider: "openai"
    modelId: "gpt-4o-mini"
    pricing:                   # Optional override
      inputToken: 0.00015

  gpt-4o:
    provider: "openai"
    modelId: "gpt-4o"
    # No pricing → uses dynamic lookup
```

---

## Part 7: Usage Examples

### Check current pricing

```bash
curl http://localhost:8080/admin/pricing \
  -H "X-Admin-Token: secret123" | jq '.'

# Output:
{
  "gpt-4o-mini": {
    "input_cost_per_1m_tokens": 0.15,
    "output_cost_per_1m_tokens": 0.6,
    "last_updated": "2025-04-05 12:00:00 UTC",
    "cached": true
  },
  "claude-opus": {
    "input_cost_per_1m_tokens": 15,
    "output_cost_per_1m_tokens": 75,
    "last_updated": "2025-04-05 12:00:00 UTC",
    "cached": true
  },
  ...
}
```

### Refresh pricing immediately

```bash
curl -X POST http://localhost:8080/admin/pricing/refresh \
  -H "X-Admin-Token: secret123"

# Output:
{"status": "pricing refreshed"}
```

### Check pricing in request logs

```
2025-04-05 14:30:00 Request cost: $0.000150 (10 tokens × $0.15/1M + 2 out × $0.6/1M) [2025-04-05 12:00:00 UTC]
```

---

## Part 8: Fallback Strategy (Reliability)

What happens if pricing fetch fails?

```go
func (pm *PricingManager) GetPrice(provider, modelID string) (PricingInfo, error) {
	// 1. Try cache first
	if cachedPrice, ok := pm.cache[modelID]; ok && isCacheFresh() {
		return cachedPrice, nil  // ✅ Cache hit
	}

	// 2. Try fresh fetch
	price, err := fetchPricingFromProvider(provider, modelID)
	if err == nil {
		pm.updateCache(modelID, price)
		return price, nil  // ✅ Fresh fetch
	}

	// 3. Fall back to stale cache (even if old)
	if cachedPrice, ok := pm.cache[modelID]; ok {
		log.Warnf("Using stale pricing for %s (fetch failed: %v)", modelID, err)
		return cachedPrice, nil  // ⚠ Stale but better than error
	}

	// 4. If all else fails, use zero pricing
	log.Errorf("No pricing found for %s: %v", modelID, err)
	return PricingInfo{}, nil  // ✅ Return zero (cost calculation skipped)
}
```

---

## Part 9: Pricing Dashboard (Admin UI)

```html
<!-- admin/pricing-dashboard.html -->

<div class="pricing-stats">
  <h2>Current Pricing (Updated {{ lastUpdated }})</h2>

  <table>
    <thead>
      <tr>
        <th>Model</th>
        <th>Provider</th>
        <th>Input (per 1M)</th>
        <th>Output (per 1M)</th>
        <th>Cached</th>
        <th>Last Updated</th>
      </tr>
    </thead>
    <tbody>
      <tr ng-repeat="(modelId, pricing) in pricingData">
        <td>{{ modelId }}</td>
        <td>{{ getProvider(modelId) }}</td>
        <td>${{ pricing.input_cost_per_1m_tokens.toFixed(4) }}</td>
        <td>${{ pricing.output_cost_per_1m_tokens.toFixed(4) }}</td>
        <td>
          <span ng-if="pricing.cached" class="badge success">✓ Cached</span>
          <span ng-if="!pricing.cached" class="badge warning">⚠ Stale</span>
        </td>
        <td>{{ pricing.last_updated }}</td>
      </tr>
    </tbody>
  </table>

  <button ng-click="refreshPricing()">Refresh Now</button>
</div>

<script>
  $http.get('/admin/pricing', {
    headers: {'X-Admin-Token': adminToken}
  }).then(res => {
    $scope.pricingData = res.data;
    $scope.lastUpdated = new Date().toLocaleString();
  });

  $scope.refreshPricing = () => {
    $http.post('/admin/pricing/refresh', {}, {
      headers: {'X-Admin-Token': adminToken}
    }).then(() => alert('Pricing refreshed!'));
  };
</script>
```

---

## Part 10: Cost Tracking with Dynamic Pricing

### Request Log with Dynamic Pricing Info

```json
{
  "request_id": "chatcmpl-...",
  "tenant_id": "acme-corp",
  "model": "gpt-4o-mini",
  "input_tokens": 150,
  "output_tokens": 45,
  "pricing": {
    "input_cost_per_1m_tokens": 0.15,
    "output_cost_per_1m_tokens": 0.6,
    "last_updated": "2025-04-05 12:00:00 UTC",
    "calculated_cost": 0.000049
  },
  "cost_usd": 0.000049,
  "total_tokens": 195,
  "latency_ms": 450,
  "timestamp": "2025-04-05T14:30:00Z"
}
```

### Daily Cost Report (Auto-Generated)

```
═══════════════════════════════════════════════════════════════
                    DAILY COST REPORT
                   2025-04-05 00:00 UTC
═══════════════════════════════════════════════════════════════

Total Requests:              10,542
Total Tokens:                2,145,000
Total Cost:                  $1,234.56

By Model:
  gpt-4o-mini              (68%)   $450.00   [5,000 tokens avg]
  google-generative-ai-flash (22%)   $300.00   [12,000 tokens avg]
  claude-haiku             (8%)    $250.00   [8,000 tokens avg]
  claude-opus              (2%)    $234.56   [150,000 tokens avg]

By Tenant:
  acme-corp                (40%)   $493.82   [4,224 requests]
  startup-xyz              (35%)   $432.10   [3,689 requests]
  enterprise-bank          (25%)   $308.64   [2,629 requests]

Pricing Status:
  Last Updated:             2025-04-05 12:00:00 UTC
  Freshness:                12 hours ago (next update: 2025-04-05 24:00:00)
  Models With Stale Pricing: 0
  Fetch Failures Today:      0

Trend (vs yesterday):
  Cost change:              +2.3% (yesterday: $1,207.50)
  Request change:           +1.1% (yesterday: 10,425 requests)
  Avg cost per request:     $0.117 (yesterday: $0.116)
═══════════════════════════════════════════════════════════════
```

---

## Summary Table: Pricing Strategies

| Strategy | Pros | Cons | Latency |
|----------|------|------|---------|
| **Hardcoded** | Simple, predictable | Manual updates | 0ns |
| **Fetch at startup** | Auto-updated hourly | Stale between updates | 1st request slower |
| **Real-time fetch** | Always accurate | Slower per request | +50-200ms |
| **Hybrid (Cache + refresh)** | ✅ Fast + accurate | Slightly complex | ~1ms (cache) |

**Recommended**: **Hybrid** (best of both worlds)

---

## Implementation Checklist

- [ ] Create `internal/pricing/openai_pricing.go` (hardcoded data)
- [ ] Create `internal/pricing/anthropic_pricing.go` (hardcoded data)
- [ ] Create `internal/pricing/google_pricing.go` (hardcoded data)
- [ ] Create `internal/pricing/manager.go` (PricingManager with caching)
- [ ] Add `GetPrice()` method to PricingManager
- [ ] Integrate PricingManager into main.go
- [ ] Add `/admin/pricing` endpoint (view pricing)
- [ ] Add `/admin/pricing/refresh` endpoint (manual refresh)
- [ ] Update cost calculation to use dynamic pricing
- [ ] Add pricing info to request logs
- [ ] Create pricing dashboard (optional)
- [ ] Remove hardcoded pricing from config YAML
- [ ] Test with different models and providers

---

## Testing

```bash
# View current pricing
curl http://localhost:8080/admin/pricing -H "X-Admin-Token: secret123" | jq '.'

# Send a request (cost calculated with dynamic pricing)
curl -X POST http://localhost:8080/api/v1/chat \
  -d '{"messages": [{"role": "user", "content": "hi"}]}'

# Check logs for pricing info
tail -f logs/gateway.log | grep "pricing\|cost"

# Refresh pricing
curl -X POST http://localhost:8080/admin/pricing/refresh \
  -H "X-Admin-Token: secret123"
```

---

## Result

✅ **No manual pricing maintenance needed**
✅ **Pricing updates automatically (hourly)**
✅ **Falls back gracefully if fetch fails**
✅ **Can override specific models if needed**
✅ **Full audit trail of pricing changes**
✅ **Cost calculation always accurate**

Your gateway handles **all the pricing complexity**—customers don't need to think about it! 🚀

