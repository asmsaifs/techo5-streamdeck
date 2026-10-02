package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAppendsAndSetsABigLogAside(t *testing.T) {
	dir := t.TempDir()
	w, c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	c.Close()
	w, c, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("two\n"))
	c.Close()
	got, _ := os.ReadFile(filepath.Join(dir, Name))
	if string(got) != "one\ntwo\n" {
		t.Fatalf("log = %q, want both lines", got)
	}

	if err := os.WriteFile(filepath.Join(dir, Name), []byte(strings.Repeat("x", maxSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, c, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if st, err := os.Stat(filepath.Join(dir, Name+".1")); err != nil || st.Size() != maxSize+1 {
		t.Fatalf("old log not set aside: %v", err)
	}
	if st, _ := os.Stat(filepath.Join(dir, Name)); st.Size() != 0 {
		t.Fatalf("new log has %d bytes, want 0", st.Size())
	}
}
