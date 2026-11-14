package api

import (
	"net/http"
	"testing"
)

func TestListProjects(t *testing.T) {
	_, srv, _, _ := testServer(t)
	var got []projectJSON
	if code := getJSON(t, srv.URL+"/api/projects", &got); code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d", code)
	}
	if len(got) != 1 || got[0].Name != "misarescu/Goodreads-Clone" || got[0].KeyPrefix != "gc" {
		t.Fatalf("projects = %+v", got)
	}
}
