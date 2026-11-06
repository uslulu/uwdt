package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteIgnoresPredictableSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions differ")
	}
	d := t.TempDir()
	target := filepath.Join(d, "target")
	profile := filepath.Join(d, "profile")
	if err := os.WriteFile(target, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, profile+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(profile, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "before" {
		t.Fatal("symlink target modified")
	}
	st, _ := os.Stat(profile)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", st.Mode().Perm())
	}
}
