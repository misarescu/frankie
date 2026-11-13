package gitproxy

import (
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestStaticResolver(t *testing.T) {
	called := false
	r, err := StaticResolver(map[string]string{"misarescu/toy": "https://github.com/misarescu/toy.git"},
		func(*http.Request) { called = true })
	if err != nil {
		t.Fatal(err)
	}
	up, err := r.Upstream(t.Context(), "misarescu/toy")
	if err != nil || up.URL.String() != "https://github.com/misarescu/toy.git" {
		t.Fatalf("Upstream = %v, %v", up.URL, err)
	}
	up.Authorize(&http.Request{Header: http.Header{}})
	if !called {
		t.Error("authorize not passed through")
	}
	if _, err := r.Upstream(t.Context(), "misarescu/other"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unknown repo: err = %v, want ErrNotExist", err)
	}
}

func TestStaticResolverValidation(t *testing.T) {
	for name, upstreams := range map[string]map[string]string{
		"credentials in URL": {"a/b": "https://user:pass@github.com/a/b.git"},
		"ssh URL":            {"a/b": "git@github.com:a/b.git"},
		"bad repo name":      {"../b": "https://github.com/a/b.git"},
	} {
		if _, err := StaticResolver(upstreams, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
