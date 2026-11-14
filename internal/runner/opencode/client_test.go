package opencode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	stream := ": comment\nid: 1\nevent: x\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\n\n\ndata: {\"tail\":true}"
	var got []string
	if err := readSSE(strings.NewReader(stream), func(d []byte) { got = append(got, string(d)) }); err != nil {
		t.Fatal(err)
	}
	want := []string{`{"a":1}`, "line1\nline2", `{"tail":true}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func TestClientDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "opencode" || p != "pw" {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/fail" {
			http.Error(w, `{"_tag":"ConflictError"}`, http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"ses_1"}}`))
	}))
	defer srv.Close()

	var out struct {
		ID string `json:"id"`
	}
	if err := newClient(srv.URL+"/", "pw").do(t.Context(), "POST", "/api/session", map[string]string{"x": "y"}, &out); err != nil || out.ID != "ses_1" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if err := newClient(srv.URL, "wrong").do(t.Context(), "GET", "/api/x", nil, nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong password: %v", err)
	}
	if err := newClient(srv.URL, "pw").do(t.Context(), "GET", "/api/fail", nil, nil); err == nil || !strings.Contains(err.Error(), "ConflictError") {
		t.Errorf("error status: %v", err)
	}
}
