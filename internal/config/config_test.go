package config

import (
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load("no-such-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:8080" || cfg.Database.Driver != "sqlite" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("JNSEC_READ_ONLY", "true")
	t.Setenv("JNSEC_LISTEN", "0.0.0.0:9000")
	t.Setenv("JNSEC_DB_DRIVER", "mysql")
	t.Setenv("JNSEC_DB_DSN", "u:p@tcp(db:3306)/bbs?parseTime=true")
	t.Setenv("JNSEC_COOKIE_SECURE", "true")
	t.Setenv("JNSEC_TRUST_PROXY", "1")
	t.Setenv("JNSEC_SESSION_SECRET", "secret-from-env")

	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:9000" {
		t.Fatalf("listen not overridden: %s", cfg.Listen)
	}
	if cfg.Database.Driver != "mysql" || cfg.Database.DSN != "u:p@tcp(db:3306)/bbs?parseTime=true" {
		t.Fatalf("database not overridden: %+v", cfg.Database)
	}
	if !cfg.CookieSecure || !cfg.TrustProxy || !cfg.ReadOnly {
		t.Fatalf("bool env not applied: %+v", cfg)
	}
	if cfg.SessionSecret != "secret-from-env" {
		t.Fatalf("secret not overridden: %s", cfg.SessionSecret)
	}
}
