package steps

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
)

type stdioMCPProcess struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	enc     *json.Encoder
	scanner *bufio.Scanner
	dead    atomic.Bool
}

var stdioProcessCache sync.Map

// stdioCacheKey scopes the stdio process cache by tenant and server alias,
// preventing tenants sharing a subprocess when aliases collide.
type stdioCacheKey struct {
	tenantID uint16
	alias    string
}

func getOrStartStdioProcess(tenantID uint16, alias string, command []string) (*stdioMCPProcess, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("mcp stdio: empty command for alias %q", alias)
	}
	key := stdioCacheKey{tenantID: tenantID, alias: alias}
	if v, ok := stdioProcessCache.Load(key); ok {
		proc := v.(*stdioMCPProcess)
		if !proc.dead.Load() {
			return proc, nil
		}
		stdioProcessCache.Delete(key)
	}

	cmd := exec.Command(command[0], command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp stdio: start %v: %w", command, err)
	}

	proc := &stdioMCPProcess{
		cmd:     cmd,
		enc:     json.NewEncoder(stdin),
		scanner: bufio.NewScanner(stdout),
	}
	go func() {
		_ = cmd.Wait()
		proc.dead.Store(true)
		stdioProcessCache.Delete(key)
	}()
	stdioProcessCache.Store(key, proc)
	return proc, nil
}

var mcpToolsRequest = map[string]any{
	"jsonrpc": "2.0",
	"id":      1,
	"method":  "tools/list",
	"params":  map[string]any{},
}

func mcpFetchToolsStdio(proc *stdioMCPProcess) ([]mcpRawTool, error) {
	if err := proc.enc.Encode(mcpToolsRequest); err != nil {
		proc.dead.Store(true)
		return nil, fmt.Errorf("mcp stdio: encode request: %w", err)
	}
	if !proc.scanner.Scan() {
		proc.dead.Store(true)
		return nil, fmt.Errorf("mcp stdio: no response from subprocess")
	}
	var resp struct {
		Result struct {
			Tools []mcpRawTool `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(proc.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("mcp stdio: parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp stdio: rpc error: %s", resp.Error.Message)
	}
	return resp.Result.Tools, nil
}
