package memdebug

import (
	"bytes"
	"log/slog"
	"runtime"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	var buf bytes.Buffer
	Log(slog.New(slog.NewTextHandler(&buf, nil)))
	out := buf.String()
	for _, key := range []string{"heap_inuse_kib=", "sys_kib=", "goroutines="} {
		if !strings.Contains(out, key) {
			t.Errorf("missing %s in %q", key, out)
		}
	}
	if runtime.GOOS == "linux" && !strings.Contains(out, "working_set_kib=") {
		t.Errorf("missing working_set_kib on linux: %q", out)
	}
}
