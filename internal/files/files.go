// Package files implements the File Manager backend: path-confined listing,
// reading, atomic writing, renaming, deleting and uploading.
package files

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	ErrOutside    = errors.New("path is outside the allowed root")
	ErrProtected  = errors.New("refusing to modify the root directory")
	ErrTooLarge   = errors.New("file is too large to edit in the browser")
	ErrBinary     = errors.New("file looks binary and cannot be edited as text")
	ErrNotRegular = errors.New("not a regular file")
	ErrNotDir     = errors.New("not a directory")
	ErrBadName    = errors.New("invalid file name")
)

// MaxEditSize is the largest file the browser editor will open.
const MaxEditSize = 2 << 20

// FS confines all operations to Root.
type FS struct {
	root     string
	realRoot string
}

// Entry is one directory item as returned to the UI.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Symlink bool      `json:"symlink"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
}

// New creates an FS rooted at root (usually "/").
func New(root string) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return nil, ErrNotDir
	}
	return &FS{root: abs, realRoot: real}, nil
}

func within(p, root string) bool {
	if root == string(filepath.Separator) {
		return true
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// Resolve maps a UI path ("/etc/nginx") to an absolute path under the root and
// rejects anything that escapes it, including through symlinks.
func (f *FS) Resolve(p string) (string, error) {
	clean := filepath.Clean(string(filepath.Separator) + filepath.FromSlash(p))
	full := filepath.Join(f.root, clean)
	if f.realRoot != string(filepath.Separator) {
		for cur := full; ; cur = filepath.Dir(cur) {
			if real, err := filepath.EvalSymlinks(cur); err == nil {
				if !within(real, f.realRoot) {
					return "", ErrOutside
				}
				break
			}
			if cur == filepath.Dir(cur) {
				break
			}
		}
	}
	return full, nil
}

// Display converts an absolute path back to a UI path.
func (f *FS) Display(full string) string {
	rel, err := filepath.Rel(f.root, full)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func (f *FS) isRoot(full string) bool { return filepath.Clean(full) == filepath.Clean(f.root) }

// List returns the entries of a directory, folders first.
func (f *FS) List(p string) (string, []Entry, error) {
	full, err := f.Resolve(p)
	if err != nil {
		return "", nil, err
	}
	des, err := os.ReadDir(full)
	if err != nil {
		return "", nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		info, err := d.Info()
		if err != nil {
			continue // vanished between ReadDir and Info
		}
		e := Entry{
			Name:    d.Name(),
			Path:    f.Display(filepath.Join(full, d.Name())),
			IsDir:   d.IsDir(),
			Symlink: info.Mode()&fs.ModeSymlink != 0,
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().UTC(),
		}
		if e.Symlink { // show what the link points at
			if st, err := os.Stat(filepath.Join(full, d.Name())); err == nil {
				e.IsDir = st.IsDir()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return f.Display(full), out, nil
}

// ReadText returns a file's contents for the editor.
func (f *FS) ReadText(p string) ([]byte, error) {
	full, err := f.Resolve(p)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	if st.Size() > MaxEditSize {
		return nil, ErrTooLarge
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, c := range head {
		if c == 0 {
			return nil, ErrBinary
		}
	}
	return b, nil
}

// WriteText saves content atomically, keeping the file's mode and owner.
func (f *FS) WriteText(p string, content []byte) error {
	full, err := f.Resolve(p)
	if err != nil {
		return err
	}
	if f.isRoot(full) {
		return ErrProtected
	}
	mode := os.FileMode(0o644)
	var uid, gid = -1, -1
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real // write through symlinks instead of replacing them
	}
	if st, err := os.Stat(full); err == nil {
		if !st.Mode().IsRegular() {
			return ErrNotRegular
		}
		mode = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".appanel-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if uid >= 0 {
		_ = tmp.Chown(uid, gid) // best effort; only root can change owner
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), full)
}

// Mkdir creates a directory (and parents).
func (f *FS) Mkdir(p string) error {
	full, err := f.Resolve(p)
	if err != nil {
		return err
	}
	if f.isRoot(full) {
		return ErrProtected
	}
	return os.MkdirAll(full, 0o755)
}

// Delete removes a file or directory tree. The root itself is protected.
func (f *FS) Delete(p string) error {
	full, err := f.Resolve(p)
	if err != nil {
		return err
	}
	if f.isRoot(full) {
		return ErrProtected
	}
	return os.RemoveAll(full)
}

// Rename moves from -> to, refusing to overwrite an existing destination.
func (f *FS) Rename(from, to string) error {
	src, err := f.Resolve(from)
	if err != nil {
		return err
	}
	dst, err := f.Resolve(to)
	if err != nil {
		return err
	}
	if f.isRoot(src) || f.isRoot(dst) {
		return ErrProtected
	}
	if _, err := os.Lstat(dst); err == nil {
		return fs.ErrExist
	}
	return os.Rename(src, dst)
}

// ValidName reports whether name is a single safe path element.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\\x00") && len(name) <= 255
}

// SaveUpload streams r into dir/name, refusing to overwrite.
func (f *FS) SaveUpload(dir, name string, r io.Reader) (string, error) {
	if !ValidName(name) {
		return "", ErrBadName
	}
	full, err := f.Resolve(dir)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(full); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", ErrNotDir
	}
	target, err := f.Resolve(f.Display(filepath.Join(full, name)))
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		os.Remove(target)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(target)
		return "", err
	}
	return f.Display(target), nil
}

// OpenDownload returns a regular file ready to be served.
func (f *FS) OpenDownload(p string) (*os.File, os.FileInfo, error) {
	full, err := f.Resolve(p)
	if err != nil {
		return nil, nil, err
	}
	fh, err := os.Open(full)
	if err != nil {
		return nil, nil, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		fh.Close()
		return nil, nil, ErrNotRegular
	}
	return fh, st, nil
}
