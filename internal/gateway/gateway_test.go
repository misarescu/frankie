package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeLiteLLM records admin requests and answers like LiteLLM.
func fakeLiteLLM(t *testing.T, status int, reply string) (*httptest.Server, *[]map[string]any, *[]string) {
	t.Helper()
	var bodies []map[string]any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-master" {
			http.Error(w, `{"error":"bad master key"}`, http.StatusUnauthorized)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		paths = append(paths, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &paths
}

func TestCreateKey(t *testing.T) {
	srv, bodies, paths := fakeLiteLLM(t, http.StatusOK, `{"key":"sk-run-123","expires":"..."}`)
	c, err := New(srv.URL+"/", "sk-master")
	if err != nil {
		t.Fatal(err)
	}
	key, err := c.CreateKey(t.Context(), KeyRequest{RunID: "r1", Models: []string{"claude-sonnet-5-5"}, MaxBudgetUSD: 2.5, TTL: 2 * time.Hour})
	if err != nil || key != "sk-run-123" {
		t.Fatalf("CreateKey = %q, %v", key, err)
	}
	b := (*bodies)[0]
	if (*paths)[0] != "/key/generate" || b["max_budget"] != 2.5 || b["duration"] != "7200s" || b["key_alias"] != "run-r1" {
		t.Errorf("request %s %v", (*paths)[0], b)
	}
	if _, err := c.CreateKey(t.Context(), KeyRequest{RunID: "r1", Alias: "run-r1-attempt-2", Models: []string{"m"}, MaxBudgetUSD: 1, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if got := (*bodies)[1]["key_alias"]; got != "run-r1-attempt-2" {
		t.Errorf("explicit alias = %v", got)
	}
	if models := b["models"].([]any); len(models) != 1 || models[0] != "claude-sonnet-5-5" {
		t.Errorf("models = %v", b["models"])
	}
}

func TestCreateKeyValidation(t *testing.T) {
	c, _ := New("http://gw", "sk-master")
	for name, in := range map[string]KeyRequest{
		"no run":    {Models: []string{"m"}, MaxBudgetUSD: 1, TTL: time.Hour},
		"no models": {RunID: "r", MaxBudgetUSD: 1, TTL: time.Hour},
		"no budget": {RunID: "r", Models: []string{"m"}, TTL: time.Hour},
		"no ttl":    {RunID: "r", Models: []string{"m"}, MaxBudgetUSD: 1},
	} {
		if _, err := c.CreateKey(t.Context(), in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New("", "k"); err == nil {
		t.Error("New accepted an empty URL")
	}
}

func TestCreateKeyErrors(t *testing.T) {
	srv, _, _ := fakeLiteLLM(t, http.StatusBadRequest, `{"error":"budget too high"}`)
	c, _ := New(srv.URL, "sk-master")
	_, err := c.CreateKey(t.Context(), KeyRequest{RunID: "r", Models: []string{"m"}, MaxBudgetUSD: 1, TTL: time.Hour})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest || !strings.Contains(se.Body, "budget too high") {
		t.Errorf("err = %v", err)
	}
	empty, _, _ := fakeLiteLLM(t, http.StatusOK, `{}`)
	c, _ = New(empty.URL, "sk-master")
	if _, err := c.CreateKey(t.Context(), KeyRequest{RunID: "r", Models: []string{"m"}, MaxBudgetUSD: 1, TTL: time.Hour}); err == nil {
		t.Error("empty key accepted")
	}
	wrong, _ := New(srv.URL, "sk-wrong")
	if _, err := wrong.CreateKey(t.Context(), KeyRequest{RunID: "r", Models: []string{"m"}, MaxBudgetUSD: 1, TTL: time.Hour}); !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Errorf("wrong master key: err = %v", err)
	}
}

func TestDeleteKeyIsIdempotent(t *testing.T) {
	srv, bodies, paths := fakeLiteLLM(t, http.StatusOK, `{"deleted_keys":["sk-run-123"]}`)
	c, _ := New(srv.URL, "sk-master")
	if err := c.DeleteKey(t.Context(), "sk-run-123"); err != nil {
		t.Fatal(err)
	}
	if (*paths)[0] != "/key/delete" || (*bodies)[0]["keys"].([]any)[0] != "sk-run-123" {
		t.Errorf("request %s %v", (*paths)[0], (*bodies)[0])
	}
	gone, _, _ := fakeLiteLLM(t, http.StatusNotFound, `{"error":"key not found"}`)
	c, _ = New(gone.URL, "sk-master")
	if err := c.DeleteKey(t.Context(), "sk-gone"); err != nil {
		t.Errorf("deleting an unknown key: %v", err)
	}
}

// D53: a session's cost is its key's spend as the gateway records it,
// found by the key's alias.
func TestKeySpend(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/key/list" || r.Header.Get("Authorization") != "Bearer sk-master" ||
			r.URL.Query().Get("return_full_object") != "true" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		query = r.URL.Query().Get("key_alias")
		if query == "run-gone" {
			_, _ = w.Write([]byte(`{"keys":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"keys":[{"key_alias":"` + query + `","spend":0.00044,"max_budget":2}],"total_count":1}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "sk-master")
	if err != nil {
		t.Fatal(err)
	}
	spend, err := c.KeySpend(t.Context(), "run-1-attempt-1&x")
	if err != nil || spend != 0.00044 || query != "run-1-attempt-1&x" {
		t.Fatalf("spend = %v, err = %v, alias sent as %q", spend, err, query)
	}
	if _, err := c.KeySpend(t.Context(), "run-gone"); !errors.Is(err, ErrNoSuchKey) {
		t.Errorf("unknown alias: %v", err)
	}
	failing, _, _ := fakeLiteLLM(t, http.StatusInternalServerError, `{"error":"db down"}`)
	c, _ = New(failing.URL, "sk-master")
	var se *StatusError
	if _, err := c.KeySpend(t.Context(), "run-1"); !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
		t.Errorf("gateway error: %v", err)
	}
}
