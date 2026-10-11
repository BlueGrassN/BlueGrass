package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func base() map[string]any {
	return map[string]any{"token_key": "dGVzdC1rZXktbm90LWZvci1wcm9kdWN0aW9uLTEyMzQ=", "source": map[string]any{"host": "127.0.0.1", "port": 3306, "database": "suntest", "user": "fixture", "password": "fixture-only"}, "sessions": map[string]any{"host": "127.0.0.1", "port": 3306, "database": "suntest", "user": "fixture", "password": "fixture-only"}}
}
func write(t *testing.T, value map[string]any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(value)
	if e := os.WriteFile(p, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestDefaultsKeepCombinedConnectionBudgetAndLoopback(t *testing.T) {
	c, e := Load(write(t, base()))
	if e != nil {
		t.Fatal(e)
	}
	if c.Source.MaxOpen != 16 || c.Sessions.MaxOpen != 16 || c.Source.MaxIdle != 8 || c.Listen != "127.0.0.1:8087" || !c.RequireHTTPS || c.HashWorkers != 8 || c.MaxInflight != 128 {
		t.Fatalf("unexpected safe defaults %+v", c)
	}
}
func TestUnsafeConfigurationFailsBeforeNetworkUse(t *testing.T) {
	for _, update := range []func(map[string]any){
		func(m map[string]any) { m["token_key"] = "weak" },
		func(m map[string]any) { m["hash_workers"] = 100000 },
		func(m map[string]any) { m["max_inflight"] = 0 },
		func(m map[string]any) { m["source"].(map[string]any)["max_open"] = 0 },
		func(m map[string]any) { m["source"].(map[string]any)["tls"] = "skip-verify" },
		func(m map[string]any) { m["session_prefix"] = "customers;DROP" },
		func(m map[string]any) { m["surprise_setting"] = true },
		func(m map[string]any) { m["listen"] = "0.0.0.0:8087"; m["require_https"] = false },
	} {
		m := base()
		update(m)
		if _, e := Load(write(t, m)); e == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
}
