package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "app.log")
	w, err := Open(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	for _, name := range []string{"app.log", "app.log.1", "app.log.2"} {
		fi, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fi.Size() > 100 {
			t.Errorf("%s is %d bytes, over the limit", name, fi.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("kept more than 2 old files: %v", err)
	}
	if _, err := w.Write([]byte("late")); err == nil {
		t.Error("write after Close succeeded")
	}
}

func TestAppendsToExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(path, []byte("old\n"), 0o600)
	w, err := Open(path, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("new\n"))
	w.Close()
	if data, _ := os.ReadFile(path); string(data) != "old\nnew\n" {
		t.Fatalf("content %q", data)
	}
}
