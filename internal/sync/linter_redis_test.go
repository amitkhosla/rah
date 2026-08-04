package sync

import (
	"testing"

	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/datasource"
)

// helper: find issues by rule name
func findIssues(issues []LintIssue, rule string) []LintIssue {
	var out []LintIssue
	for _, i := range issues {
		if i.Rule == rule {
			out = append(out, i)
		}
	}
	return out
}

// --- redis-source-required ---

func TestLint_RedisSourceRequired_MissingKey(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "myflow",
				Instructions: []control.StepConfig{
					{Action: "redis_get", Key: "", Value: "mykey"},
				},
			}},
		},
		SourceMap: SourceMap{"myflow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-source-required")
	if len(found) == 0 {
		t.Error("expected redis-source-required issue, got none")
	}
}

func TestLint_RedisSourceRequired_WithKey(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "myflow",
				Instructions: []control.StepConfig{
					{Action: "redis_get", Key: "mysource", Value: "mykey"},
				},
			}},
		},
		SourceMap: SourceMap{"myflow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-source-required")
	if len(found) != 0 {
		t.Errorf("expected no redis-source-required issues, got %d", len(found))
	}
}

func TestLint_RedisSourceRequired_NonRedisStep(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "myflow",
				Instructions: []control.StepConfig{
					{Action: "cache_get", Key: ""},
				},
			}},
		},
		SourceMap: SourceMap{"myflow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-source-required")
	if len(found) != 0 {
		t.Errorf("cache_get should not trigger redis-source-required, got %d issues", len(found))
	}
}

// --- named-query-unknown ---

func TestLint_NamedQueryUnknown_Missing(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "db_query", Value: "query:get_order"},
				},
			}},
			Queries: map[string]datasource.NamedQueryConfig{},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "named-query-unknown")
	if len(found) == 0 {
		t.Error("expected named-query-unknown issue")
	}
}

func TestLint_NamedQueryUnknown_Present(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "db_query", Value: "query:get_order"},
				},
			}},
			Queries: map[string]datasource.NamedQueryConfig{
				"get_order": {SQL: "SELECT * FROM orders WHERE id = $1"},
			},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "named-query-unknown")
	if len(found) != 0 {
		t.Errorf("expected no named-query-unknown, got %d", len(found))
	}
}

func TestLint_NamedQueryUnknown_LiteralSQL(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "db_query", Value: "SELECT 1"},
				},
			}},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "named-query-unknown")
	if len(found) != 0 {
		t.Errorf("literal SQL should not trigger named-query-unknown, got %d", len(found))
	}
}

// --- migration-version-duplicate ---

func TestLint_MigrationVersionDuplicate(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Migrations: []control.MigrationDef{
				{Version: 1, Name: "create_users", SQL: "CREATE TABLE users (id BIGSERIAL)"},
				{Version: 1, Name: "duplicate", SQL: "CREATE TABLE dup (id INT)"},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "migration-version-duplicate")
	if len(found) == 0 {
		t.Error("expected migration-version-duplicate issue")
	}
}

func TestLint_MigrationVersionInvalid(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Migrations: []control.MigrationDef{
				{Version: 0, Name: "bad", SQL: "SELECT 1"},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "migration-version-invalid")
	if len(found) == 0 {
		t.Error("expected migration-version-invalid for version=0")
	}
}

func TestLint_MigrationVersionsUnique(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Migrations: []control.MigrationDef{
				{Version: 1, Name: "v1", SQL: "CREATE TABLE a (id INT)"},
				{Version: 2, Name: "v2", SQL: "ALTER TABLE a ADD COLUMN name TEXT"},
				{Version: 3, Name: "v3", SQL: "CREATE INDEX ON a (name)"},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	dup := findIssues(issues, "migration-version-duplicate")
	inv := findIssues(issues, "migration-version-invalid")
	if len(dup)+len(inv) != 0 {
		t.Errorf("expected no migration issues, got %d dup + %d invalid", len(dup), len(inv))
	}
}

// --- redis-multi-key-no-vars ---

func TestLint_RedisMultiKeyNoVars_MissingVars(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "redis_mget", Key: "src"},
				},
			}},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-multi-key-no-vars")
	if len(found) == 0 {
		t.Error("expected redis-multi-key-no-vars issue")
	}
}

func TestLint_RedisMultiKeyNoVars_WithVars(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "redis_mget", Key: "src", Vars: []string{"key1", "key2"}},
				},
			}},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-multi-key-no-vars")
	if len(found) != 0 {
		t.Errorf("expected no redis-multi-key-no-vars, got %d", len(found))
	}
}

// --- named-query-batch-by-invalid ---

func TestLint_NamedQueryBatchBy_Invalid(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Queries: map[string]datasource.NamedQueryConfig{
				"q1": {SQL: "SELECT * FROM t WHERE id = $1", BatchBy: "$2"},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "named-query-batch-by-invalid")
	if len(found) == 0 {
		t.Error("expected named-query-batch-by-invalid: $2 not in SQL")
	}
}

func TestLint_NamedQueryBatchBy_Valid(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Queries: map[string]datasource.NamedQueryConfig{
				"q1": {SQL: "SELECT * FROM t WHERE id = ANY($1::bigint[])", BatchBy: "$1"},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "named-query-batch-by-invalid")
	if len(found) != 0 {
		t.Errorf("expected no batch-by issues, got %d", len(found))
	}
}

// --- redis-zadd-no-score ---

func TestLint_RedisZAddNoScore_Warning(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "redis_zadd", Key: "src", Score: 0},
				},
			}},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-zadd-no-score")
	if len(found) == 0 {
		t.Error("expected redis-zadd-no-score warning for score=0")
	}
	if found[0].Severity != SeverityWarning {
		t.Errorf("expected severity=warning, got %q", found[0].Severity)
	}
}

func TestLint_RedisZAddNoScore_WithScore(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "f",
				Instructions: []control.StepConfig{
					{Action: "redis_zadd", Key: "src", Score: 1.5},
				},
			}},
		},
		SourceMap: SourceMap{"f": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "redis-zadd-no-score")
	if len(found) != 0 {
		t.Errorf("expected no score warning when score=1.5, got %d", len(found))
	}
}

// --- empty bundle ---

func TestLint_EmptyBundle_WithFlow(t *testing.T) {
	// Create a minimal valid bundle to avoid Level 0 empty_bundle error
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "dummy",
				Instructions: []control.StepConfig{
					{Action: "return", Status: 200},
				},
			}},
		},
		SourceMap: SourceMap{"dummy": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	// Should not panic and should have no Redis-related issues
	redisIssues := findIssues(issues, "redis-source-required")
	if len(redisIssues) != 0 {
		t.Errorf("minimal bundle should have no redis issues, got %d", len(redisIssues))
	}
}

// --- Integration: Multiple rules together ---

func TestLint_MultipleRedisRules(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "complex_flow",
				Instructions: []control.StepConfig{
					// This one is OK: has key and score
					{Action: "redis_zadd", Key: "src1", Score: 1.5, Value: "data"},
					// This one triggers redis-zadd-no-score
					{Action: "redis_zadd", Key: "src2", Score: 0, Value: "data"},
					// This one triggers redis-source-required
					{Action: "redis_get", Key: "", Value: "key"},
					// This one triggers redis-multi-key-no-vars
					{Action: "redis_mget", Key: "src3"},
				},
			}},
		},
		SourceMap: SourceMap{"complex_flow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)

	noScore := findIssues(issues, "redis-zadd-no-score")
	if len(noScore) != 1 {
		t.Errorf("expected 1 redis-zadd-no-score, got %d", len(noScore))
	}

	sourceReq := findIssues(issues, "redis-source-required")
	if len(sourceReq) != 1 {
		t.Errorf("expected 1 redis-source-required, got %d", len(sourceReq))
	}

	multiKey := findIssues(issues, "redis-multi-key-no-vars")
	if len(multiKey) != 1 {
		t.Errorf("expected 1 redis-multi-key-no-vars, got %d", len(multiKey))
	}
}

// --- Integration: Nested steps ---

func TestLint_NestedRedisSteps(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "nested_flow",
				Instructions: []control.StepConfig{{
					Action: "foreach",
					Key:    "items",
					Do: []control.StepConfig{
						{Action: "redis_get", Key: "", Value: "key"}, // Missing key inside loop
					},
				}},
			}},
		},
		SourceMap: SourceMap{"nested_flow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)

	found := findIssues(issues, "redis-source-required")
	if len(found) == 0 {
		t.Error("expected redis-source-required in nested step")
	}
}

// --- Integration: Query validation with flow ---

func TestLint_NamedQueryWithFlowAndMigration(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{{
				Name: "query_flow",
				Instructions: []control.StepConfig{
					{Action: "db_query", Key: "db1", Value: "query:get_users"},
				},
			}},
			Queries: map[string]datasource.NamedQueryConfig{
				"get_users": {SQL: "SELECT * FROM users WHERE status = $1", BatchBy: "$1"},
			},
			Migrations: []control.MigrationDef{
				{Version: 1, Name: "initial", SQL: "CREATE TABLE users (id BIGSERIAL PRIMARY KEY)"},
			},
		},
		SourceMap: SourceMap{"query_flow": {File: "test.yaml", Line: 1}},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)

	// No errors expected for valid config
	queryUnknown := findIssues(issues, "named-query-unknown")
	if len(queryUnknown) != 0 {
		t.Errorf("expected no named-query-unknown, got %d", len(queryUnknown))
	}

	migrationInvalid := findIssues(issues, "migration-version-invalid")
	if len(migrationInvalid) != 0 {
		t.Errorf("expected no migration-version-invalid, got %d", len(migrationInvalid))
	}

	batchByInvalid := findIssues(issues, "named-query-batch-by-invalid")
	if len(batchByInvalid) != 0 {
		t.Errorf("expected no named-query-batch-by-invalid, got %d", len(batchByInvalid))
	}
}
