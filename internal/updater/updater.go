// Package updater checks GitHub for a newer Appanel release and installs it:
// it downloads the release archive, verifies its SHA-256 against the
// published checksums, test-runs the new binary, then atomically replaces the
// installed one (keeping the old one as appanel.prev for a manual rollback).
package updater

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Repo       = "usmanawa2153-beep/appanel"
	maxArchive = 200 << 20
	maxBinary  = 200 << 20
)

// OneLiner upgrades (or installs) from a shell, keeping data and settings.
const OneLiner = "curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | sudo bash"

var tagRe = regexp.MustCompile(`^v(\d{1,4})\.(\d{1,4})\.(\d{1,6})$`)

// Info is what the UI shows.
type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	CanUpdate bool   `json:"can_update"` // false for dev builds
	Notes     string `json:"notes,omitempty"`
	URL       string `json:"url,omitempty"`
	Published string `json:"published,omitempty"`
	OneLiner  string `json:"one_liner"`
}

// Updater talks to GitHub and replaces the binary.
type Updater struct {
	Current string
	APIBase string // https://api.github.com
	DLBase  string // https://github.com
	HTTP    *http.Client
	Exe     string // path of the installed binary
	Arch    string
	// Probe runs a candidate binary with "version" and returns its output.
	Probe func(ctx context.Context, path string) (string, error)

	mu       sync.Mutex
	cached   *Info
	cachedAt time.Time
}

// New returns an Updater using the real GitHub endpoints.
func New(current string) *Updater {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return &Updater{
		Current: current, APIBase: "https://api.github.com", DLBase: "https://github.com",
		HTTP: &http.Client{Timeout: 5 * time.Minute}, Exe: exe, Arch: runtime.GOARCH, Probe: probe,
	}
}

func probe(ctx context.Context, path string) (string, error) {
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	return string(out), err
}

// parse splits v1.2.3 into numbers.
func parse(tag string) ([3]int, bool) {
	m := tagRe.FindStringSubmatch(tag)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Newer reports whether tag is a higher version than current.
func Newer(tag, current string) bool {
	a, ok1 := parse(tag)
	b, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "appanel-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := u.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("download is larger than expected")
	}
	return b, nil
}

// Check asks GitHub for the latest release. Results are cached for ten
// minutes unless force is set.
func (u *Updater) Check(ctx context.Context, force bool) (Info, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && u.cached != nil && time.Since(u.cachedAt) < 10*time.Minute {
		return *u.cached, nil
	}
	b, err := u.get(ctx, u.APIBase+"/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return Info{}, err
	}
	var rel struct {
		Tag       string `json:"tag_name"`
		Body      string `json:"body"`
		URL       string `json:"html_url"`
		Published string `json:"published_at"`
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return Info{}, errors.New("unexpected reply from GitHub")
	}
	if _, ok := parse(rel.Tag); !ok {
		return Info{}, fmt.Errorf("the latest release has an unexpected version name %q", rel.Tag)
	}
	notes := rel.Body
	if len(notes) > 2000 {
		notes = notes[:2000] + "…"
	}
	_, curOK := parse(u.Current)
	info := Info{Current: u.Current, Latest: rel.Tag, Available: Newer(rel.Tag, u.Current), CanUpdate: curOK,
		Notes: notes, URL: rel.URL, Published: rel.Published, OneLiner: OneLiner}
	u.cached, u.cachedAt = &info, time.Now()
	return info, nil
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive does not contain the appanel binary")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && strings.TrimPrefix(h.Name, "./") == "appanel" {
			b, err := io.ReadAll(io.LimitReader(tr, maxBinary+1))
			if err != nil {
				return nil, err
			}
			if len(b) > maxBinary {
				return nil, errors.New("binary is larger than expected")
			}
			return b, nil
		}
	}
}

func checksumFor(sums []byte, asset string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// Apply installs the latest release over the running binary. The caller
// restarts the service afterwards. It returns the version installed.
func (u *Updater) Apply(ctx context.Context) (string, error) {
	info, err := u.Check(ctx, true)
	if err != nil {
		return "", err
	}
	if !info.CanUpdate {
		return "", errors.New("this is a development build; update it by installing a release")
	}
	if !info.Available {
		return "", errors.New("already on the latest version")
	}
	if u.Arch != "amd64" && u.Arch != "arm64" {
		return "", fmt.Errorf("no release is built for %s", u.Arch)
	}
	asset := "appanel-linux-" + u.Arch + ".tar.gz"
	base := u.DLBase + "/" + Repo + "/releases/download/" + info.Latest + "/"
	archive, err := u.get(ctx, base+asset, maxArchive)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	sums, err := u.get(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return "", fmt.Errorf("could not download checksums: %w", err)
	}
	want, ok := checksumFor(sums, asset)
	if !ok {
		return "", errors.New("the release has no checksum for this download; refusing to install")
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != want {
		return "", errors.New("checksum mismatch; refusing to install")
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(u.Exe)
	tmp, err := os.CreateTemp(dir, ".appanel-new-*")
	if err != nil {
		return "", fmt.Errorf("cannot write next to %s: %w", u.Exe, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// Run it once before it replaces the working binary.
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := u.Probe(pctx, tmp.Name())
	if err != nil || !strings.Contains(out, info.Latest) {
		return "", errors.New("the downloaded binary did not start correctly; the current version was left in place")
	}
	if old, err := os.ReadFile(u.Exe); err == nil {
		_ = os.WriteFile(u.Exe+".prev", old, 0o755)
	}
	if err := os.Rename(tmp.Name(), u.Exe); err != nil {
		return "", err
	}
	u.mu.Lock()
	u.cached = nil
	u.mu.Unlock()
	return info.Latest, nil
}
