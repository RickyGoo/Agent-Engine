package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"agent-engine/internal/model"
	"agent-engine/internal/secret"
)

type AnthropicClient struct {
	NameValue string
	Endpoint  string
	SecretRef model.SecretRef
	Secrets   secret.Store
	HTTP      *http.Client
}

func NewAnthropicClient(endpoint string, ref model.SecretRef, store secret.Store) *AnthropicClient {
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	return &AnthropicClient{
		NameValue: "anthropic",
		Endpoint:  endpoint,
		SecretRef: ref,
		Secrets:   store,
		HTTP:      NewHTTPClient(),
	}
}

func (c *AnthropicClient) Name() string {
	return c.NameValue
}

func (c *AnthropicClient) HealthCheck(ctx context.Context) error {
	if _, err := c.resolveKey(ctx); err != nil {
		return err
	}
	parsed, err := url.Parse(c.Endpoint)
	if err != nil {
		return err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid endpoint: %s", c.Endpoint)
	}
	return nil
}

func (c *AnthropicClient) CompleteJSON(ctx context.Context, model, systemPrompt, userPrompt string, out any) error {
	key, err := c.resolveKey(ctx)
	if err != nil {
		return fmt.Errorf("resolve api key for model %q at %s: %w", model, c.Endpoint, err)
	}
	body := map[string]any{
		"model":       model,
		"system":      systemPrompt,
		"messages":    []map[string]any{{"role": "user", "content": userPrompt}},
		"max_tokens":  4096,
		"temperature": 0,
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("provider request failed (provider=anthropic endpoint=%s model=%s): %w", c.Endpoint, model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("provider request failed (provider=anthropic endpoint=%s model=%s): %s: %s", c.Endpoint, model, resp.Status, strings.TrimSpace(string(payload)))
	}

	var decoded struct {
		Content []anthropicContentBlock `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	content := joinAnthropicText(decoded.Content)
	if content == "" {
		return fmt.Errorf("provider response empty content (provider=anthropic endpoint=%s model=%s)", c.Endpoint, model)
	}
	return json.Unmarshal([]byte(content), out)
}

func (c *AnthropicClient) ProbeJSON(ctx context.Context, model string) error {
	var out struct {
		OK bool `json:"ok"`
	}
	systemPrompt := "You are a connectivity probe. Return only JSON."
	userPrompt := `{"ok":true}`
	if err := c.CompleteJSON(ctx, model, systemPrompt, userPrompt, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("provider probe returned ok=false (provider=anthropic endpoint=%s model=%s)", c.Endpoint, model)
	}
	return nil
}

func (c *AnthropicClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return NewHTTPClient()
}

func (c *AnthropicClient) resolveKey(ctx context.Context) (string, error) {
	if c.Secrets == nil {
		return "", errors.New("secret store is required")
	}
	return c.Secrets.Resolve(ctx, secret.Ref(c.SecretRef))
}
