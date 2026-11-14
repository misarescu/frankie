package pi

import (
	"slices"
	"strings"
	"testing"
)

func TestTools(t *testing.T) {
	tests := []struct {
		allowed  []string
		want     []string
		readonly bool
	}{
		{nil, nil, false},
		{[]string{"read", "glob", "edit", "write", "bash"}, []string{"read", "find", "ls", "edit", "write", "bash"}, false},
		{[]string{"read", "grep", "bash:readonly"}, []string{"read", "grep", "bash"}, true},
		{[]string{"edit", "write", "edit"}, []string{"edit", "write"}, false},
	}
	for _, tt := range tests {
		got, readonly, err := tools(tt.allowed)
		if err != nil || !slices.Equal(got, tt.want) || readonly != tt.readonly {
			t.Errorf("tools(%v) = %v, %v, %v; want %v, %v", tt.allowed, got, readonly, err, tt.want, tt.readonly)
		}
	}
	for name, allowed := range map[string][]string{"web fetch": {"web-fetch"}, "unknown": {"kubectl"}, "both shells": {"bash", "bash:readonly"}} {
		if _, _, err := tools(allowed); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadonlyExtensionReadsTheSharedList(t *testing.T) {
	src := string(readonlyExtension)
	for _, want := range []string{readonlyEnv, `"tool_call"`, `event.toolName !== "bash"`, "block: true"} {
		if !strings.Contains(src, want) {
			t.Errorf("readonly.ts lacks %q", want)
		}
	}
}
