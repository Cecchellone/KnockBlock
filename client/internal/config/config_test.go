package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != DefaultHost || c.Port != DefaultPort || !c.SendAppNames || !c.Notifications {
		t.Fatalf("defaults %+v", c)
	}
	if c.Address() != "knockblock.local:6053" {
		t.Fatalf("Address %q", c.Address())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "KnockBlock", "config.json")
	c := Default()
	c.Host = "192.168.1.50"
	c.PCName = "PC-STUDIO"
	c.SendAppNames = false
	c.Ignore = []string{"obs64.exe"}
	c.AppNames = map[string]string{"foo.exe": "Foo"}
	if err := c.SetKey("AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != c.Host || got.PCName != c.PCName || got.SendAppNames || got.Ignore[0] != "obs64.exe" || got.AppNames["foo.exe"] != "Foo" {
		t.Fatalf("round trip %+v", got)
	}
	key, err := got.Key()
	if err != nil || key != "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" {
		t.Fatalf("Key = %q, %v", key, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o077 != 0 && !strings.Contains(os.Getenv("OS"), "Windows") {
		t.Errorf("config readable by others: %v", fi.Mode())
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"host": "light.lan", "port": 0}`), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "light.lan" || c.Port != DefaultPort || !c.SendAppNames {
		t.Fatalf("got %+v", c)
	}
}

func TestCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{not json`), 0o600)
	c, err := Load(path)
	if err == nil {
		t.Fatal("corrupt file accepted")
	}
	if c.Host != DefaultHost {
		t.Fatalf("corrupt file should give defaults, got %+v", c)
	}
}

func TestNoKey(t *testing.T) {
	c := Default()
	if k, err := c.Key(); k != "" || err != nil {
		t.Fatalf("Key = %q, %v", k, err)
	}
	c.SetKey("x")
	c.SetKey("")
	if c.KeyProtected != "" {
		t.Fatal("SetKey(\"\") did not clear the key")
	}
}

func TestProtectPlainKey(t *testing.T) {
	c := Default()
	if changed, err := c.ProtectPlainKey(); changed || err != nil {
		t.Fatalf("no plain key: %v, %v", changed, err)
	}
	c.KeyPlain = "secret"
	changed, err := c.ProtectPlainKey()
	if !changed || err != nil || c.KeyPlain != "" {
		t.Fatalf("changed %v, err %v, plain %q", changed, err, c.KeyPlain)
	}
	if k, _ := c.Key(); k != "secret" {
		t.Fatalf("Key = %q", k)
	}
}
