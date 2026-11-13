// Package gateway is the control plane's client for the LLM gateway's admin API
// (LiteLLM, docs/spec/04-agents-harnesses-models.md). It mints one short-lived
// virtual key per run, with a budget and an allowed model list, so real provider
// keys never reach an agent pod (invariant 1, FR-SB-4).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the gateway's admin API with the master key.
type Client struct {
	baseURL   string
	masterKey string
	http      *http.Client
}

// New returns a client for the gateway at baseURL, e.g. http://frankie-llm-gateway.frankie.svc:4000.
func New(baseURL, masterKey string) (*Client, error) {
	if baseURL == "" || masterKey == "" {
		return nil, errors.New("gateway: base URL and master key are required")
	}
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), masterKey: masterKey, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

// KeyRequest describes a run's virtual key.
type KeyRequest struct {
	RunID        string
	Alias        string        // unique across keys; default "run-<RunID>"
	Models       []string      // the only models the key may call
	MaxBudgetUSD float64       // spend cap enforced by the gateway
	TTL          time.Duration // the key expires after this
}

// CreateKey mints a virtual key for a run.
func (c *Client) CreateKey(ctx context.Context, in KeyRequest) (string, error) {
	switch {
	case in.RunID == "":
		return "", errors.New("gateway: run id is required")
	case len(in.Models) == 0:
		return "", errors.New("gateway: at least one model is required")
	case in.MaxBudgetUSD <= 0:
		return "", errors.New("gateway: a positive budget is required")
	case in.TTL < time.Second:
		return "", errors.New("gateway: ttl must be at least a second")
	}
	alias := in.Alias
	if alias == "" {
		alias = "run-" + in.RunID
	}
	body := map[string]any{
		"models":     in.Models,
		"max_budget": in.MaxBudgetUSD,
		"duration":   fmt.Sprintf("%ds", int(in.TTL.Seconds())),
		"key_alias":  alias,
		"metadata":   map[string]string{"frankie_run_id": in.RunID},
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := c.post(ctx, "/key/generate", body, &out); err != nil {
		return "", err
	}
	if out.Key == "" {
		return "", errors.New("gateway: key/generate returned no key")
	}
	return out.Key, nil
}

// DeleteKey revokes a virtual key. Deleting an unknown key is not an error, so
// retried cleanup Activities stay idempotent.
func (c *Client) DeleteKey(ctx context.Context, key string) error {
	err := c.post(ctx, "/key/delete", map[string]any{"keys": []string{key}}, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// StatusError is a non-2xx gateway response.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("gateway: status %d: %s", e.Code, e.Body)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("gateway: encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("gateway: %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gateway: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("gateway: decode %s: %w", path, err)
		}
	}
	return nil
}
