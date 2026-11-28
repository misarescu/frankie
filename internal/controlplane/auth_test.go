package controlplane

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

func TestLoadAuthConfigValidation(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("valid auth config", func(t *testing.T) {
		p := write(t, `{"auth":{"publicURL":"https://frankie.example.com/","sessionTTL":"24h","allowlist":{"emailDomains":["acme.io"],"defaultRole":"member"},"sso":{"github":{"enabled":true}}}}`)
		var cfg Config
		if err := LoadConfigFile(p, &cfg); err != nil {
			t.Fatalf("LoadConfigFile: %v", err)
		}
		if cfg.Auth.PublicURL != "https://frankie.example.com" || cfg.Auth.Allowlist.DefaultRole != domain.RoleMember || !cfg.Auth.GitHubEnabled {
			t.Fatalf("unexpected auth config: %+v", cfg.Auth)
		}
	})

	t.Run("rejects previewMode with publicURL", func(t *testing.T) {
		p := write(t, `{"auth":{"previewMode":true,"publicURL":"https://frankie.example.com"}}`)
		var cfg Config
		if err := LoadConfigFile(p, &cfg); err == nil {
			t.Fatal("expected error for previewMode + publicURL")
		}
	})

	t.Run("rejects admin allowlist defaultRole", func(t *testing.T) {
		p := write(t, `{"auth":{"allowlist":{"emailDomains":["acme.io"],"defaultRole":"admin"}}}`)
		var cfg Config
		if err := LoadConfigFile(p, &cfg); err == nil {
			t.Fatal("expected error for admin defaultRole")
		}
	})
}

func TestRunBootstrapAdminIdempotent(t *testing.T) {
	dbURL := storetest.DatabaseURL(t)
	st, err := store.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(t.Context(), st.Pool()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := runBootstrapAdmin(t.Context(), st, BootstrapAdminConfig{
		Username: "admin", Password: "bootstrap strong password", Email: "admin@example.com",
	}, log); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	// Second run with different username must be a no-op once any user exists.
	if err := runBootstrapAdmin(t.Context(), st, BootstrapAdminConfig{
		Username: "second", Password: "another strong password",
	}, log); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	users, err := st.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "admin" || users[0].Role != domain.RoleAdmin {
		t.Fatalf("users = %+v, want only admin", users)
	}
}
