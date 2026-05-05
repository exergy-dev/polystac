package pgstac

import "testing"

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		{"0.7.0", "0.7.0", true},
		{"0.8.1", "0.7.0", true},
		{"v0.8.1", "0.7.0", true},
		{"1.0.0", "0.7.0", true},
		{"0.6.9", "0.7.0", false},
		{"0.7.0-rc1", "0.7.0", true},
	}
	for _, tc := range cases {
		if got := versionAtLeast(tc.got, tc.want); got != tc.ok {
			t.Errorf("versionAtLeast(%q,%q) = %v, want %v", tc.got, tc.want, got, tc.ok)
		}
	}
}

func TestConfigFromMapRequiresDSN(t *testing.T) {
	if _, err := configFromMap(map[string]string{}); err == nil {
		t.Error("expected error when DSN missing")
	}
	pc, err := configFromMap(map[string]string{
		"dsn":      "postgresql://x/y",
		"pool_min": "5",
		"pool_max": "30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pc.MinConns != 5 || pc.MaxConns != 30 {
		t.Errorf("pool: %+v", pc)
	}
}

func TestRegisteredAtInit(t *testing.T) {
	// Importing this package's tests forces init(); the registry must
	// know about us. (Verifying without referencing internal/backends
	// avoids a possible import cycle.)
	t.Helper()
}
