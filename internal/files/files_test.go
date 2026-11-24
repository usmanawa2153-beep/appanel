package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFS(t *testing.T) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	f, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return f, root
}

func TestTraversalIsConfined(t *testing.T) {
	f, root := newFS(t)
	secret := filepath.Join(filepath.Dir(root), "outside-secret.txt")
	if err := os.WriteFile(secret, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)
	for _, p := range []string{"../outside-secret.txt", "/../../outside-secret.txt", "a/../../outside-secret.txt"} {
		if _, err := f.ReadText(p); err == nil {
			t.Errorf("%q escaped the root", p)
		}
	}
}

func TestSymlinkEscapeBlocked(t *testing.T) {
	f, root := newFS(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadText("/link/x.txt"); !errors.Is(err, ErrOutside) {
		t.Fatalf("symlink escape not blocked: %v", err)
	}
	if err := f.WriteText("/link/new.txt", []byte("x")); !errors.Is(err, ErrOutside) {
		t.Fatalf("write through symlink not blocked: %v", err)
	}
}

func TestWriteReadRoundTripKeepsMode(t *testing.T) {
	f, root := newFS(t)
	p := filepath.Join(root, "run.sh")
	if err := os.WriteFile(p, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteText("/run.sh", []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, err := f.ReadText("/run.sh")
	if err != nil || string(b) != "new" {
		t.Fatalf("got %q, %v", b, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o750 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
}

func TestBinaryAndLargeRefused(t *testing.T) {
	f, root := newFS(t)
	os.WriteFile(filepath.Join(root, "bin"), []byte{1, 2, 0, 3}, 0o644)
	if _, err := f.ReadText("/bin"); !errors.Is(err, ErrBinary) {
		t.Fatalf("want ErrBinary, got %v", err)
	}
	os.WriteFile(filepath.Join(root, "big"), []byte(strings.Repeat("a", MaxEditSize+1)), 0o644)
	if _, err := f.ReadText("/big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestRootProtected(t *testing.T) {
	f, _ := newFS(t)
	if err := f.Delete("/"); !errors.Is(err, ErrProtected) {
		t.Fatalf("root delete: %v", err)
	}
	if err := f.Delete("/../.."); !errors.Is(err, ErrProtected) {
		t.Fatalf("root delete via ..: %v", err)
	}
}

func TestRenameNoOverwrite(t *testing.T) {
	f, root := newFS(t)
	os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "b"), []byte("b"), 0o644)
	if err := f.Rename("/a", "/b"); err == nil {
		t.Fatal("rename overwrote an existing file")
	}
	if err := f.Rename("/a", "/c"); err != nil {
		t.Fatal(err)
	}
}

func TestUploadNameValidationAndNoOverwrite(t *testing.T) {
	f, root := newFS(t)
	for _, n := range []string{"", "..", "a/b", `a\b`, "x\x00y"} {
		if _, err := f.SaveUpload("/", n, strings.NewReader("x")); !errors.Is(err, ErrBadName) {
			t.Errorf("name %q accepted", n)
		}
	}
	if _, err := f.SaveUpload("/", "ok.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SaveUpload("/", "ok.txt", strings.NewReader("again")); err == nil {
		t.Fatal("upload overwrote existing file")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "ok.txt")); string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
}

func TestListSortsDirsFirst(t *testing.T) {
	f, root := newFS(t)
	os.WriteFile(filepath.Join(root, "b.txt"), nil, 0o644)
	os.Mkdir(filepath.Join(root, "zdir"), 0o755)
	os.WriteFile(filepath.Join(root, "A.txt"), nil, 0o644)
	_, es, err := f.List("/")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{es[0].Name, es[1].Name, es[2].Name}
	want := []string{"zdir", "A.txt", "b.txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
