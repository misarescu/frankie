package opencode

import (
	"bufio"
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

// client calls a session's private opencode server. The server listens on
// loopback only and requires its random password (HTTP basic auth, user
// "opencode").
type client struct {
	base     string
	password string
	http     *http.Client // requests; the event stream has no timeout
}

func newClient(base, password string) *client {
	return &client{base: strings.TrimSuffix(base, "/"), password: password, http: &http.Client{Timeout: 30 * time.Second}}
}

// do sends a JSON request and decodes the response's data into out, if given.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("opencode: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("opencode: %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("opencode: %s %s: %s: %s", method, path, resp.Status, truncate(string(raw), 512))
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("opencode: %s %s: decode: %w", method, path, err)
	}
	return json.Unmarshal(env.Data, out)
}

// ready waits until the server answers requests: it prints its address
// slightly before its routes are up.
func (c *client) ready(ctx context.Context, timeout time.Duration, exited <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for {
		err := c.do(ctx, http.MethodGet, "/api/info", nil, nil)
		if err == nil {
			return nil
		}
		select {
		case <-exited:
			return errors.New("opencode: server exited during startup")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("opencode: server not ready after %s: %w", timeout, err)
		}
	}
}

// events opens the server's event stream. The stream is volatile: events
// sent while it is not connected are lost, so it is opened before the first
// prompt.
func (c *client) events(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/event", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", c.password)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req) // no timeout: it lasts the session
	if err != nil {
		return nil, fmt.Errorf("opencode: event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("opencode: event stream: %s", resp.Status)
	}
	return resp.Body, nil
}

// readSSE calls handle with the data of each server-sent event until r ends.
func readSSE(r io.Reader, handle func(data []byte)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var data []byte
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if len(data) > 0 {
				handle(data)
				data = nil
			}
		case bytes.HasPrefix(line, []byte("data:")):
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))...)
		}
		// id:, event: and comment lines carry nothing the runner needs.
	}
	if len(data) > 0 {
		handle(data)
	}
	return sc.Err()
}
