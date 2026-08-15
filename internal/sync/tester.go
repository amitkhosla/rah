package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TestDef is one test case defined in a bundle's tests/ directory YAML.
type TestDef struct {
	Name       string          `json:"name"       yaml:"name"`
	FlowName   string          `json:"flow_name"  yaml:"flow_name"`
	Mode       string          `json:"mode"       yaml:"mode"`       // "published" (default)
	CallMode   string          `json:"call_mode"  yaml:"call_mode"`  // "real" (default) | "mock" | "schema_only"
	Input      TestInputDef    `json:"input"      yaml:"input"`
	Mocks      []TestMockDef   `json:"mocks"      yaml:"mocks"`
	Assertions []TestAssertDef `json:"assertions" yaml:"assertions"`
}

type TestInputDef struct {
	Method    string            `json:"method"     yaml:"method"`
	Path      string            `json:"path"       yaml:"path"`
	Headers   map[string]string `json:"headers"    yaml:"headers"`
	Body      string            `json:"body"       yaml:"body"`
	TenantKey string            `json:"tenant_key" yaml:"tenant_key"`
}

type TestMockDef struct {
	StepName string `json:"step_name" yaml:"step_name"`
	Status   int    `json:"status"    yaml:"status"`
	Body     string `json:"body"      yaml:"body"`
}

type TestAssertDef struct {
	Type     string `json:"type"     yaml:"type"`
	Expected any    `json:"expected" yaml:"expected"`
	Path     string `json:"path"     yaml:"path"`
	Max      int    `json:"max"      yaml:"max"`
	Step     string `json:"step"     yaml:"step"`
	Key      string `json:"key"      yaml:"key"`
}

// TestFile is parsed from a tests/*.yaml file — top-level key is "tests".
type TestFile struct {
	Tests []TestDef `json:"tests" yaml:"tests"`
}

// TestResult is the outcome of one test.
type TestResult struct {
	Name       string
	Passed     bool
	DurationMs float64
	Status     int
	Error      string
	Assertions []AssertResult
}

// AssertResult is one assertion outcome.
type AssertResult struct {
	Type    string
	Passed  bool
	Message string
}

// LoadTests recursively walks dir/tests/ and returns all TestDefs found.
func LoadTests(dir string) ([]TestDef, error) {
	testsDir := filepath.Join(dir, "tests")
	info, err := os.Stat(testsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no tests directory — not an error
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", testsDir)
	}

	var all []TestDef
	err = filepath.WalkDir(testsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var tf TestFile
		if err := yaml.Unmarshal(data, &tf); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		all = append(all, tf.Tests...)
		return nil
	})
	return all, err
}

// RunTests executes each test against the studio server and returns results.
func RunTests(studioURL, token string, tests []TestDef) []TestResult {
	results := make([]TestResult, 0, len(tests))
	for _, t := range tests {
		r := runOne(studioURL, token, t)
		results = append(results, r)
	}
	return results
}

func runOne(studioURL, token string, t TestDef) TestResult {
	res := TestResult{Name: t.Name}

	if t.Mode == "" {
		t.Mode = "published"
	}
	if t.CallMode == "" {
		t.CallMode = "real"
	}
	if t.Input.Method == "" {
		t.Input.Method = "GET"
	}

	body, _ := json.Marshal(map[string]any{
		"flow_name":  t.FlowName,
		"mode":       t.Mode,
		"call_mode":  t.CallMode,
		"input":      t.Input,
		"mocks":      t.Mocks,
		"assertions": t.Assertions,
	})

	req, err := http.NewRequest("POST", studioURL+"/api/test/execute", bytes.NewReader(body))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	res.DurationMs = float64(time.Since(start).Milliseconds())

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		res.Error = fmt.Sprintf("gateway returned %d: %s", resp.StatusCode, string(respBytes))
		return res
	}

	var execResp struct {
		Passed     bool    `json:"passed"`
		DurationMs float64 `json:"duration_ms"`
		Response   struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"response"`
		Assertions []struct {
			Type    string `json:"type"`
			Passed  bool   `json:"passed"`
			Message string `json:"message"`
		} `json:"assertions"`
	}
	if err := json.Unmarshal(respBytes, &execResp); err != nil {
		res.Error = fmt.Sprintf("parse response: %v", err)
		return res
	}

	res.Passed = execResp.Passed
	res.DurationMs = execResp.DurationMs
	res.Status = execResp.Response.Status
	for _, a := range execResp.Assertions {
		res.Assertions = append(res.Assertions, AssertResult{
			Type:    a.Type,
			Passed:  a.Passed,
			Message: a.Message,
		})
	}
	return res
}

// PrintTestResults prints a human-readable test run summary to w.
func PrintTestResults(results []TestResult) (passed, failed int) {
	for _, r := range results {
		if r.Passed {
			passed++
			fmt.Printf("  ✓ %s (%.1fms)\n", r.Name, r.DurationMs)
		} else {
			failed++
			fmt.Printf("  ✗ %s", r.Name)
			if r.Error != "" {
				fmt.Printf(" — %s", r.Error)
			}
			fmt.Println()
			for _, a := range r.Assertions {
				if !a.Passed {
					fmt.Printf("      [%s] %s\n", a.Type, a.Message)
				}
			}
		}
	}
	return
}
