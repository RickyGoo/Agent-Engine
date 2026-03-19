package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client interface {
	Name() string
	HealthCheck(ctx context.Context) error
	CompleteJSON(ctx context.Context, model, systemPrompt, userPrompt string, out any) error
}

type FixedClient struct {
	ClientName string
	Handler    func(ctx context.Context, model, systemPrompt, userPrompt string, out any) error
}

func (f FixedClient) Name() string { return f.ClientName }

func (f FixedClient) HealthCheck(context.Context) error { return nil }

func (f FixedClient) CompleteJSON(ctx context.Context, model, systemPrompt, userPrompt string, out any) error {
	if f.Handler == nil {
		return fmt.Errorf("fixed provider %s has no handler", f.ClientName)
	}
	return f.Handler(ctx, model, systemPrompt, userPrompt, out)
}

type JSONResponder func(model, systemPrompt, userPrompt string) (json.RawMessage, error)

type DefaultHTTPClient struct {
	*http.Client
}

func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}
