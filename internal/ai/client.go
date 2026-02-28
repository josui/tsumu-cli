// cli/internal/ai/client.go

// Package ai 封装 Gemini API 调用，提供描述生成、标签推荐、搜索展开功能。
// 使用 REST API 直接调用，不依赖 Google SDK。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://generativelanguage.googleapis.com/v1beta/models"

// Client 是 Gemini API 客户端。
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewClient 创建 Gemini API 客户端。
func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ── 请求/响应结构 ──

type generateRequest struct {
	Contents         []content         `json:"contents"`
	GenerationConfig *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
}

type generateResponse struct {
	Candidates []candidate `json:"candidates"`
}

type candidate struct {
	Content candidateContent `json:"content"`
}

type candidateContent struct {
	Parts []part `json:"parts"`
}

// Generate 调用 Gemini generateContent API，返回文本响应。
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.generateWithConfig(ctx, prompt, nil)
}

// GenerateJSON 调用 Gemini API 并要求返回 JSON 格式。
func (c *Client) GenerateJSON(ctx context.Context, prompt string) (string, error) {
	cfg := &generationConfig{
		ResponseMIMEType: "application/json",
	}
	return c.generateWithConfig(ctx, prompt, cfg)
}

func (c *Client) generateWithConfig(ctx context.Context, prompt string, cfg *generationConfig) (string, error) {
	reqBody := generateRequest{
		Contents: []content{
			{Parts: []part{{Text: prompt}}},
		},
		GenerationConfig: cfg,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent", baseURL, c.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp generateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(genResp.Candidates) == 0 || len(genResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	return genResp.Candidates[0].Content.Parts[0].Text, nil
}
