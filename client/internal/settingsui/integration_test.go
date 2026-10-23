//go:build integration

package settingsui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/Cecchellone/KnockBlock/client/internal/config"
	"github.com/Cecchellone/KnockBlock/client/internal/i18n"
	"github.com/Cecchellone/KnockBlock/client/locales"
)

// The Light tab reads and writes the settings of the real host firmware
// (scripts/itest-host.sh): display language and the flip switch stand-in.
func TestIntegrationDeviceSettings(t *testing.T) {
	addr := os.Getenv("KNOCKBLOCK_ITEST_ADDR")
	if addr == "" {
		t.Skip("KNOCKBLOCK_ITEST_ADDR not set")
	}
	host, port, _ := net.SplitHostPort(addr)
	cfg := config.Default()
	cfg.Host = host
	cfg.Port, _ = strconv.Atoi(port)
	if k := os.Getenv("KNOCKBLOCK_ITEST_KEY"); k != "" {
		cfg.SetKey(k)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	cat, _ := i18n.Load(locales.FS)
	m := Load(Deps{ConfigPath: path, Catalog: cat, Autostart: &fakeAutostart{}})

	d, err := m.OpenDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := d.Settings()
	if !slices.Equal(s.Languages, []string{"en", "it"}) || !s.HasFlip || s.HasBacklight {
		t.Fatalf("settings %+v", s)
	}
	s.Flip, s.Language = !s.Flip, "it"
	if err := d.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d, err = m.OpenDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := d.Settings(); got.Flip != s.Flip || got.Language != "it" {
		t.Fatalf("after apply: %+v", got)
	}
	// Leave the device as the other tests expect it.
	s.Flip, s.Language = false, "en"
	if err := d.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}
