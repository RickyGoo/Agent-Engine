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

type GeminiClient struct {
	NameValue string
	Endpoint  string
	SecretRef model.SecretRef
	Secrets   secret.Store
	HTTP      *http.Client
}

func NewGeminiClient(endpoint string, ref model.SecretRef, store secret.Store) *GeminiClient {
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &GeminiClient{
		NameValue: "gemini",
		Endpoint:  endpoint,
		SecretRef: ref,
		Secrets:   store,
		HTTP:      NewHTTPClient(),
	}
}

func (c *GeminiClient) Name() string {
	return c.NameValue
}

func (c *GeminiClient) HealthCheck(ctx context.Context) error {
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

func (c *GeminiClient) CompleteJSON(ctx context.Context, model, systemPrompt, userPrompt string, out any) error {
	key, err := c.resolveKey(ctx)
	if err != nil {
		return fmt.Errorf("resolve api key for model %q at %s: %w", model, c.Endpoint, err)
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimRight(c.Endpoint, "/"), url.PathEscape(model))
	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": userPrompt}},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0,
			"maxOutputTokens":  4096,
			"responseMimeType": "application/json",
		},
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("provider request failed (provider=gemini endpoint=%s model=%s): %w", c.Endpoint, model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("provider request failed (provider=gemini endpoint=%s model=%s): %s: %s", c.Endpoint, model, resp.Status, strings.TrimSpace(string(payload)))
	}

	var decoded struct {
		Candidates []geminiCandidate `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	content := joinGeminiText(decoded.Candidates)
	if content == "" {
		return fmt.Errorf("provider response empty content (provider=gemini endpoint=%s model=%s)", c.Endpoint, model)
	}
	return json.Unmarshal([]byte(content), out)
}

func (c *GeminiClient) ProbeJSON(ctx context.Context, model string) error {
	var out struct {
		OK bool `json:"ok"`
	}
	systemPrompt := "You are a connectivity probe. Return only JSON."
	userPrompt := `{"ok":true}`
	if err := c.CompleteJSON(ctx, model, systemPrompt, userPrompt, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("provider probe returned ok=false (provider=gemini endpoint=%s model=%s)", c.Endpoint, model)
	}
	return nil
}

func (c *GeminiClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return NewHTTPClient()
}

func (c *GeminiClient) resolveKey(ctx context.Context) (string, error) {
	if c.Secrets == nil {
		return "", errors.New("secret store is required")
	}
	return c.Secrets.Resolve(ctx, secret.Ref(c.SecretRef))
}
