package config

import "testing"

func TestDefaults(t *testing.T) {
	c, err := Load(nil, map[string]string{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Backend != "inmem" {
		t.Errorf("backend default: %q", c.Backend)
	}
	if c.Server.Listen != ":8000" {
		t.Errorf("listen default: %q", c.Server.Listen)
	}
	if c.Search.DefaultLimit != 10 {
		t.Errorf("default_limit: %d", c.Search.DefaultLimit)
	}
}

func TestEnvOverrideAndAlias(t *testing.T) {
	c, err := Load(nil, map[string]string{
		"STAC_FASTAPI_TITLE": "Hello",
		"POLYSTAC_BACKEND":   "pgstac",
		"POLYSTAC_PG_DSN":    "postgresql://x/y",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Landing.Title != "Hello" {
		t.Errorf("title: %q", c.Landing.Title)
	}
	if c.Backend != "pgstac" {
		t.Errorf("backend: %q", c.Backend)
	}
	if c.BackendConfig["dsn"] != "postgresql://x/y" {
		t.Errorf("backend config: %+v", c.BackendConfig)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	c, err := Load([]string{"-listen", ":9000"}, map[string]string{
		"POLYSTAC_LISTEN": ":7000",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Server.Listen != ":9000" {
		t.Errorf("listen: %q", c.Server.Listen)
	}
}

func TestValidationCatchesBadLimits(t *testing.T) {
	_, err := Load([]string{"-default-limit", "100", "-max-limit", "10"}, map[string]string{})
	if err == nil {
		t.Error("expected validation error")
	}
}
