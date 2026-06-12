package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The Codeberg release endpoints `botfile upgrade` resolves against: the same
// source of truth the installer uses, so the two can never disagree about what
// "latest" means.
const (
	releaseAPI    = "https://codeberg.org/api/v1/repos/botfile/botfile/releases/latest"
	releaseDL     = "https://codeberg.org/botfile/botfile/releases/download"
	installerHint = "curl -fsSL https://botfile.org/install.sh | sh"
)

// upgradeDeps are the boundary ports behind `botfile upgrade`: the network
// fetch, the path of the running binary, and the platform the asset name is
// built from. Injected so tests run without network, without replacing the
// test binary, and can exercise the windows rename path on any OS.
type upgradeDeps struct {
	fetch   func(url string) ([]byte, error)
	exePath func() (string, error)
	goos    string
	goarch  string
}

// upgradeReport is the JSON form of the run, following the report envelope's
// convention that exitCode is authoritative.
type upgradeReport struct {
	SchemaVersion int    `json:"schemaVersion"`
	Command       string `json:"command"`
	Outcome       string `json:"outcome"`
	Current       string `json:"current"`
	Latest        string `json:"latest"`
	UpToDate      bool   `json:"upToDate"`
	Applied       bool   `json:"applied"`
	Detail        string `json:"detail,omitempty"`
	ExitCode      int    `json:"exitCode"`
}

// upgradeCmd handles `botfile upgrade`: resolve the latest published release,
// compare it with this binary's version, and replace the binary in place
// after a sha256 match against the release's checksums.txt (`--check` stops
// at reporting). It is the only botfile verb that touches the network, it
// does so only when invoked (botfile never checks in the background), and it
// mutates nothing but botfile's own binary. Like guide and version it loads
// no config.
//
// Exit codes follow the contract: 0 success (up to date, checked, or
// upgraded), 1 blocked (a non-release build refuses to self-replace), 2 a
// network, verification, or filesystem failure.
func upgradeCmd(w io.Writer, rest []string, deps upgradeDeps) int {
	checkOnly, format, ok := upgradeArgs(rest)
	if !ok {
		return 2
	}

	body, err := deps.fetch(releaseAPI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "botfile: resolve latest release: %v\n", err)
		return 2
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		fmt.Fprintln(os.Stderr, "botfile: resolve latest release: no tag in the release metadata")
		return 2
	}

	r := upgradeReport{SchemaVersion: 1, Command: "upgrade", Outcome: "ok", Current: version, Latest: rel.TagName}
	upToDate, comparable := releaseCompare(version, rel.TagName)
	r.UpToDate = upToDate

	switch {
	case upToDate:
		r.Detail = "already the latest release"
	case !comparable:
		// A "dev" or otherwise non-release build: replacing it could be a
		// downgrade, and a source build's owner upgrades at the source.
		r.Detail = "not a release build; upgrade via `go install ./cmd/botfile` from your checkout, or install the release: " + installerHint
		if !checkOnly {
			r.Outcome, r.ExitCode = "blocked", 1
		}
	case checkOnly:
		r.Detail = "newer release available; run `botfile upgrade` to apply"
	default:
		if err := applyUpgrade(deps, rel.TagName); err != nil {
			fmt.Fprintf(os.Stderr, "botfile: upgrade to %s: %v\n", rel.TagName, err)
			return 2
		}
		r.Applied = true
		r.Detail = "binary replaced after checksum verification"
	}

	if format == "json" {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Fprintln(w, string(b))
		return r.ExitCode
	}
	switch {
	case r.Applied:
		fmt.Fprintf(w, "upgraded botfile %s -> %s\n", version, rel.TagName)
	case upToDate:
		fmt.Fprintf(w, "botfile %s is the latest release\n", version)
	case !comparable:
		fmt.Fprintf(w, "botfile %s is not a release build; the latest release is %s\n%s\n", version, rel.TagName, r.Detail)
	default:
		fmt.Fprintf(w, "botfile %s -> %s is available; run `botfile upgrade` to apply\n", version, rel.TagName)
	}
	return r.ExitCode
}

// upgradeArgs parses upgrade's flags: --check and --format text|json.
func upgradeArgs(rest []string) (checkOnly bool, format string, ok bool) {
	format = "text"
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		switch {
		case tok == "--check":
			checkOnly = true
		case strings.HasPrefix(tok, "--format="):
			format = strings.TrimPrefix(tok, "--format=")
		case tok == "--format":
			i++
			if i >= len(rest) {
				fmt.Fprintln(os.Stderr, "botfile: flag \"--format\" needs a value")
				return false, "", false
			}
			format = rest[i]
		default:
			fmt.Fprintf(os.Stderr, "botfile: unexpected argument %q\n", tok)
			return false, "", false
		}
	}
	if format != "text" && format != "json" {
		fmt.Fprintf(os.Stderr, "botfile: --format must be one of text|json, got %q\n", format)
		return false, "", false
	}
	return checkOnly, format, true
}

// applyUpgrade downloads the platform asset for tag, verifies its sha256
// against the release's checksums.txt, and atomically replaces the running
// binary. The new binary lands as a temp file in the same directory first, so
// the final step is a rename and a failure can never leave a half-written
// botfile on PATH.
func applyUpgrade(deps upgradeDeps, tag string) error {
	asset := "botfile-" + deps.goos + "-" + deps.goarch
	if deps.goos == "windows" {
		asset += ".exe"
	}

	sums, err := deps.fetch(releaseDL + "/" + tag + "/checksums.txt")
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	want, ok := checksumFor(string(sums), asset)
	if !ok {
		return fmt.Errorf("no checksum for %s in the release's checksums.txt", asset)
	}

	bin, err := deps.fetch(releaseDL + "/" + tag + "/" + asset)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: the download does not match the release manifest; keeping the current binary", asset)
	}

	exe, err := deps.exePath()
	if err != nil {
		return fmt.Errorf("locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".botfile-upgrade-*")
	if err != nil {
		return permHint(fmt.Errorf("stage the new binary next to %s: %w", exe, err))
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return fmt.Errorf("write the new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("mark the new binary executable: %w", err)
	}

	// Windows cannot replace a running executable in place; rename it aside
	// first (the .old file of a running process cannot be deleted, so it is
	// left behind and overwritten by the next upgrade).
	if deps.goos == "windows" {
		old := strings.TrimSuffix(exe, ".exe") + ".old.exe"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return permHint(fmt.Errorf("move the running binary aside: %w", err))
		}
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return permHint(fmt.Errorf("install the new binary: %w", err))
	}
	return nil
}

// permHint decorates a permission failure with the two ways out: privilege,
// or the installer (which can choose a writable directory).
func permHint(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w\nre-run with elevated privileges, or use the installer: %s", err, installerHint)
	}
	return err
}

// checksumFor finds asset's hex digest in a sha256sum-format manifest
// ("<hex>  <name>" per line).
func checksumFor(sums, asset string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], true
		}
	}
	return "", false
}

// releaseCompare reports whether current is at least latest (upToDate) and
// whether both were comparable vX.Y.Z release tags. A version that does not
// parse (a "dev" snapshot, say) is never up to date and not comparable, so
// the caller can word the outcome honestly rather than urge what might be a
// downgrade.
func releaseCompare(current, latest string) (upToDate, comparable bool) {
	if current == latest {
		return true, true
	}
	cur, okC := parseRelease(current)
	lat, okL := parseRelease(latest)
	if !okC || !okL {
		return false, false
	}
	for i := range cur {
		if cur[i] != lat[i] {
			return cur[i] > lat[i], true
		}
	}
	return true, true
}

// parseRelease parses a vX.Y.Z release tag into its three numbers.
func parseRelease(v string) ([3]int, bool) {
	var out [3]int
	rest, found := strings.CutPrefix(v, "v")
	parts := strings.Split(rest, ".")
	if !found || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || p == "" {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// osUpgradeDeps wires the real boundary: HTTP with a bounded timeout, the
// process's own executable path, and the build platform.
func osUpgradeDeps() upgradeDeps {
	return upgradeDeps{
		fetch:   httpFetch,
		exePath: os.Executable,
		goos:    runtime.GOOS,
		goarch:  runtime.GOARCH,
	}
}

// httpFetch GETs url and returns the body; a non-200 status is an error. The
// timeout bounds the whole request, so a stalled network cannot hang the cli.
func httpFetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
