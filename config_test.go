package errorgap

import (
	"errors"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	t.Setenv("ERRORGAP_ENDPOINT", "")
	t.Setenv("ERRORGAP_PROJECT_SLUG", "")
	t.Setenv("ERRORGAP_PROJECT_ID", "")
	t.Setenv("ERRORGAP_API_KEY", "")

	cfg := Config{}
	cfg.applyDefaults()

	if cfg.Endpoint != "http://127.0.0.1:3030" {
		t.Errorf("Endpoint default = %q, want http://127.0.0.1:3030", cfg.Endpoint)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment default = %q, want production", cfg.Environment)
	}
	if len(cfg.FilterKeys) == 0 {
		t.Error("FilterKeys default should be non-empty")
	}
	if cfg.QueueSize != 100 {
		t.Errorf("QueueSize default = %d, want 100", cfg.QueueSize)
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	t.Setenv("ERRORGAP_ENDPOINT", "https://errorgap.example.com")
	t.Setenv("ERRORGAP_PROJECT_SLUG", "demo")
	t.Setenv("ERRORGAP_PROJECT_ID", "p_123")
	t.Setenv("ERRORGAP_API_KEY", "flk_test")

	cfg := Config{}
	cfg.applyDefaults()

	if cfg.Endpoint != "https://errorgap.example.com" {
		t.Errorf("Endpoint = %q, want env value", cfg.Endpoint)
	}
	if cfg.ProjectSlug != "demo" {
		t.Errorf("ProjectSlug = %q", cfg.ProjectSlug)
	}
	if cfg.APIKey != "flk_test" {
		t.Errorf("APIKey = %q", cfg.APIKey)
	}
}

func TestConfigExplicitOverridesEnv(t *testing.T) {
	t.Setenv("ERRORGAP_PROJECT_SLUG", "from-env")
	cfg := Config{ProjectSlug: "from-arg"}
	cfg.applyDefaults()
	if cfg.ProjectSlug != "from-arg" {
		t.Errorf("ProjectSlug = %q, explicit should win", cfg.ProjectSlug)
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := Config{Endpoint: "https://e.example.com"}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrMissingProjectSlug) {
		t.Errorf("validate without slug = %v, want ErrMissingProjectSlug", err)
	}

	cfg2 := Config{ProjectSlug: "demo", Endpoint: "https://e.example.com"}
	cfg2.applyDefaults()
	if err := cfg2.validate(); err != nil {
		t.Errorf("validate with slug+endpoint = %v, want nil", err)
	}
}
