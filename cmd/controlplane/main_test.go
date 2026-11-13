package main

import "testing"

func TestParseRepos(t *testing.T) {
	got, err := parseRepos(" misarescu/toy=https://github.com/misarescu/toy.git , a/b=https://x/a/b.git,")
	if err != nil || len(got) != 2 || got["misarescu/toy"] != "https://github.com/misarescu/toy.git" || got["a/b"] != "https://x/a/b.git" {
		t.Fatalf("parseRepos = %v, %v", got, err)
	}
	if got, err := parseRepos(""); err != nil || len(got) != 0 {
		t.Errorf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"noequals", "=https://x", "a/b="} {
		if _, err := parseRepos(bad); err == nil {
			t.Errorf("parseRepos(%q) accepted", bad)
		}
	}
}
