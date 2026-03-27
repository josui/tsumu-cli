// Package sync 实现 Turso 云端同步。
// 使用 Hrana over HTTP 协议（POST /v2/pipeline）与 Turso 通信，
// 本地保持纯 SQLite，不依赖 embedded replica。
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── Arg 自定义 JSON 序列化 ──
// Turso Hrana API 对 null 和非 null 类型的 value 字段要求不同：
//   - null 类型：不应包含 value 字段 → {"type":"null"}
//   - 其他类型：必须包含 value 字段（即使值为空字符串）→ {"type":"text","value":""}
// Go 的 omitempty 会把空字符串也省略，导致 TextArg("") 变成 {"type":"text"} 被 API 拒绝。

func (a Arg) MarshalJSON() ([]byte, error) {
	if a.Type == "null" {
		return []byte(`{"type":"null"}`), nil
	}
	// 非 null 类型：始终包含 value 字段
	type argWithValue struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	return json.Marshal(argWithValue{a.Type, a.Value})
}

// Client 是 Turso HTTP API 客户端。
// 通过 Hrana over HTTP 协议发送 SQL 语句到远端 Turso 数据库。
type Client struct {
	baseURL   string // Turso 数据库 URL（libsql:// 转换为 https://）
	authToken string
	http      *http.Client
}

// NewClient 创建 Turso HTTP API 客户端。
// dbURL 格式：libsql://xxx.turso.io 或 https://xxx.turso.io
func NewClient(dbURL, authToken string) *Client {
	// libsql:// → https://
	base := dbURL
	if len(base) > 9 && base[:9] == "libsql://" {
		base = "https://" + base[9:]
	}
	return &Client{
		baseURL:   base,
		authToken: authToken,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── Hrana over HTTP 请求/响应结构 ──

// pipelineRequest 是 /v2/pipeline 的请求体。
type pipelineRequest struct {
	Requests []request `json:"requests"`
}

// request 是 pipeline 中的单个请求。
type request struct {
	Type string `json:"type"`           // "execute" | "close"
	Stmt *stmt  `json:"stmt,omitempty"` // type=execute 时必填
}

// Stmt 是 SQL 语句及其参数。对外暴露用于构造批量请求。
type Stmt struct {
	SQL  string `json:"sql"`
	Args []Arg  `json:"args,omitempty"`
}

// stmt 是内部使用的 SQL 语句结构（与 Stmt 相同，用于 JSON 序列化）。
type stmt = Stmt

// Arg 是 SQL 参数值。Turso HTTP API 要求所有值都以 type+value 形式传递。
type Arg struct {
	Type  string `json:"type"`            // "null" | "integer" | "float" | "text"
	Value string `json:"value,omitempty"` // 所有类型都用字符串传递，null 时省略
}

// TextArg 创建 text 类型参数。
func TextArg(v string) Arg {
	return Arg{Type: "text", Value: v}
}

// IntArg 创建 integer 类型参数。
func IntArg(v int) Arg {
	return Arg{Type: "integer", Value: fmt.Sprintf("%d", v)}
}

// NullArg 创建 null 类型参数。
func NullArg() Arg {
	return Arg{Type: "null"}
}

// pipelineResponse 是 /v2/pipeline 的响应体。
type pipelineResponse struct {
	Results []resultWrapper `json:"results"`
}

// resultWrapper 包装单个结果。
type resultWrapper struct {
	Type     string    `json:"type"` // "ok" | "error"
	Response *response `json:"response,omitempty"`
	Error    *apiError `json:"error,omitempty"`
}

// response 是执行结果。
type response struct {
	Type   string        `json:"type"` // "execute"
	Result ExecuteResult `json:"result"`
}

// ExecuteResult 是 SQL 执行结果。
type ExecuteResult struct {
	Cols             []Col     `json:"cols"`
	Rows             [][]Value `json:"rows"`
	AffectedRowCount int       `json:"affected_row_count"`
}

// Col 是列定义。
type Col struct {
	Name     string `json:"name"`
	Decltype string `json:"decltype"`
}

// Value 是单元格值。
type Value struct {
	Type  string `json:"type"`  // "null" | "integer" | "float" | "text"
	Value string `json:"value"`
}

// apiError 是 API 错误。
type apiError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// Execute 执行一组 SQL 语句，返回各语句的执行结果。
// 所有语句在同一个 pipeline 中执行，最后自动追加 close。
func (c *Client) Execute(ctx context.Context, stmts []Stmt) ([]ExecuteResult, error) {
	reqs := make([]request, 0, len(stmts)+1)
	for i := range stmts {
		reqs = append(reqs, request{Type: "execute", Stmt: &stmts[i]})
	}
	reqs = append(reqs, request{Type: "close"})

	body, err := json.Marshal(pipelineRequest{Requests: reqs})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v2/pipeline"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var pResp pipelineResponse
	if err := json.Unmarshal(respBody, &pResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// 提取执行结果（跳过最后的 close 响应）
	var results []ExecuteResult
	for _, r := range pResp.Results {
		if r.Type == "error" && r.Error != nil {
			return nil, fmt.Errorf("remote SQL error: %s (code: %s)", r.Error.Message, r.Error.Code)
		}
		if r.Response != nil && r.Response.Type == "execute" {
			results = append(results, r.Response.Result)
		}
	}

	return results, nil
}

// ExecuteOne 执行单条 SQL 语句，返回结果。
func (c *Client) ExecuteOne(ctx context.Context, sql string, args ...Arg) (*ExecuteResult, error) {
	results, err := c.Execute(ctx, []Stmt{{SQL: sql, Args: args}})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no execution result")
	}
	return &results[0], nil
}
