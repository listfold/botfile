package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withVersion swaps the package version for one test. The tests that use it
// must not run in parallel, since version is package state.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// fakeRelease wires an in-memory release: the API metadata, the checksums
// manifest, and the platform asset, keyed by the exact URLs upgradeCmd hits.
func fakeRelease(tag, asset string, bin []byte) map[string][]byte {
	sum := sha256.Sum256(bin)
	return map[string][]byte{
		releaseAPI: []byte(`{"tag_name": "` + tag + `"}`),
		releaseDL + "/" + tag + "/checksums.txt": []byte(
			hex.EncodeToString(sum[:]) + "  " + asset + "\n"),
		releaseDL + "/" + tag + "/" + asset: bin,
	}
}

// fetchFrom serves the fake release and records the limit each URL was
// fetched with, so tests can pin the per-endpoint caps.
func fetchFrom(m map[string][]byte, limits map[string]int64) func(string, int64) ([]byte, error) {
	return func(url string, limit int64) ([]byte, error) {
		if limits != nil {
			limits[url] = limit
		}
		b, ok := m[url]
		if !ok {
			return nil, fmt.Errorf("GET %s: HTTP 404", url)
		}
		return b, nil
	}
}

// testDeps builds deps over a fake release for a linux/amd64 binary at exe.
func testDeps(m map[string][]byte, exe string) upgradeDeps {
	return upgradeDeps{
		fetch:   fetchFrom(m, nil),
		exePath: func() (string, error) { return exe, nil },
		rename:  os.Rename,
		goos:    "linux",
		goarch:  "amd64",
	}
}

func TestUpgradeCheckReportsNewer(t *testing.T) {
	withVersion(t, "v0.1.0")
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new"))
	var buf strings.Builder
	if code := upgradeCmd(&buf, []string{"--check"}, testDeps(m, "/nonexistent")); code != 0 {
		t.Fatalf("check exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "v0.1.0 -> v0.2.0 is available") {
		t.Errorf("check output = %q, want the available line", buf.String())
	}
}

func TestUpgradeCheckJSON(t *testing.T) {
	withVersion(t, "v0.1.0")
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new"))
	var buf strings.Builder
	if code := upgradeCmd(&buf, []string{"--check", "--format", "json"}, testDeps(m, "/nonexistent")); code != 0 {
		t.Fatalf("check exit = %d, want 0", code)
	}
	var r upgradeReport
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		t.Fatalf("check json: %v\n%s", err, buf.String())
	}
	if r.Command != "upgrade" || r.Phase != "done" || r.Outcome != "ok" ||
		r.Current != "v0.1.0" || r.Latest != "v0.2.0" || r.UpToDate || !r.Comparable || r.Applied || r.ExitCode != 0 {
		t.Errorf("report = %+v, want a clean newer-available check", r)
	}
}

func TestUpgradeUpToDate(t *testing.T) {
	withVersion(t, "v0.2.0")
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("same"))
	var buf strings.Builder
	if code := upgradeCmd(&buf, nil, testDeps(m, "/nonexistent")); code != 0 {
		t.Fatalf("up-to-date exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "is the latest release") {
		t.Errorf("output = %q, want the latest-release line", buf.String())
	}
}

func TestUpgradeRefusesNonReleaseBuild(t *testing.T) {
	withVersion(t, "dev")
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new"))
	var buf strings.Builder
	// A check is informational: exit 0.
	if code := upgradeCmd(&buf, []string{"--check"}, testDeps(m, "/nonexistent")); code != 0 {
		t.Fatalf("dev check exit = %d, want 0", code)
	}
	// An apply is blocked: the replacement could be a downgrade.
	buf.Reset()
	if code := upgradeCmd(&buf, []string{"--format", "json"}, testDeps(m, "/nonexistent")); code != 1 {
		t.Fatalf("dev apply exit = %d, want 1 (blocked)", code)
	}
	var r upgradeReport
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		t.Fatalf("blocked json: %v\n%s", err, buf.String())
	}
	if r.Outcome != "blocked" || r.ExitCode != 1 || r.Comparable || r.Applied {
		t.Errorf("report = %+v, want a blocked non-release apply", r)
	}
	if !strings.Contains(r.Detail, "not a release build") {
		t.Errorf("detail = %q, want the non-release wording", r.Detail)
	}
}

// TestDefaultVersionIsNotARelease pins finding-2's invariant: a plain
// `go install ./cmd/botfile` build (no ldflags) must identify as a
// non-release, so upgrade refuses to replace it.
func TestDefaultVersionIsNotARelease(t *testing.T) {
	if _, comparable := releaseCompare(version, "v9.9.9"); comparable {
		t.Fatalf("baked default version %q compares as a release build; it must be a dev marker", version)
	}
}

func TestUpgradeAppliesAndReplacesBinary(t *testing.T) {
	withVersion(t, "v0.1.0")
	dir := t.TempDir()
	exe := filepath.Join(dir, "botfile")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new binary bytes"))
	limits := map[string]int64{}
	deps := testDeps(m, exe)
	deps.fetch = fetchFrom(m, limits)
	var buf strings.Builder
	if code := upgradeCmd(&buf, nil, deps); code != 0 {
		t.Fatalf("apply exit = %d, want 0", code)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "new binary bytes" {
		t.Fatalf("binary after upgrade = %q, %v; want the new bytes", got, err)
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode = %v, want 0755", info.Mode().Perm())
	}
	if !strings.Contains(buf.String(), "upgraded botfile v0.1.0 -> v0.2.0") {
		t.Errorf("output = %q, want the upgraded line", buf.String())
	}
	// The staging temp file must be gone either way.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".botfile-upgrade-") {
			t.Errorf("staging file %s left behind", e.Name())
		}
	}
	// Per-endpoint caps: small for metadata and manifest, large for the binary.
	if limits[releaseAPI] != maxMetaBytes || limits[releaseDL+"/v0.2.0/checksums.txt"] != maxMetaBytes {
		t.Errorf("metadata limits = %v, want %d", limits, int64(maxMetaBytes))
	}
	if limits[releaseDL+"/v0.2.0/botfile-linux-amd64"] != maxBinaryBytes {
		t.Errorf("binary limit = %v, want %d", limits, int64(maxBinaryBytes))
	}
}

func TestUpgradeAppliedJSON(t *testing.T) {
	withVersion(t, "v0.1.0")
	dir := t.TempDir()
	exe := filepath.Join(dir, "botfile")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new"))
	var buf strings.Builder
	if code := upgradeCmd(&buf, []string{"--format=json"}, testDeps(m, exe)); code != 0 {
		t.Fatalf("apply exit = %d, want 0", code)
	}
	var r upgradeReport
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		t.Fatalf("applied json: %v\n%s", err, buf.String())
	}
	if r.Outcome != "ok" || !r.Applied || r.ExitCode != 0 {
		t.Errorf("report = %+v, want an applied ok run", r)
	}
}

func TestUpgradeWindowsRenamesAside(t *testing.T) {
	withVersion(t, "v0.1.0")
	dir := t.TempDir()
	exe := filepath.Join(dir, "botfile.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fakeRelease("v0.2.0", "botfile-windows-amd64.exe", []byte("new"))
	deps := testDeps(m, exe)
	deps.goos = "windows"
	var buf strings.Builder
	if code := upgradeCmd(&buf, nil, deps); code != 0 {
		t.Fatalf("apply exit = %d, want 0", code)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Errorf("binary after upgrade = %q, want new", got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "botfile.old.exe")); err != nil || string(got) != "old" {
		t.Errorf("old binary aside = %q, %v; want the old bytes preserved", got, err)
	}
}

// TestUpgradeWindowsRollbackRestoresBinary forces the second rename (staged
// -> exe) to fail and requires the original binary back at its path: a failed
// upgrade must never leave botfile missing from PATH.
func TestUpgradeWindowsRollbackRestoresBinary(t *testing.T) {
	withVersion(t, "v0.1.0")
	dir := t.TempDir()
	exe := filepath.Join(dir, "botfile.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fakeRelease("v0.2.0", "botfile-windows-amd64.exe", []byte("new"))
	deps := testDeps(m, exe)
	deps.goos = "windows"
	deps.rename = func(oldpath, newpath string) error {
		// The install step moves the staged temp file onto the exe path.
		// (Match by basename: EvalSymlinks may canonicalize the directory,
		// e.g. /var -> /private/var on macOS.)
		if filepath.Base(newpath) == "botfile.exe" && strings.HasPrefix(filepath.Base(oldpath), ".botfile-upgrade-") {
			return errors.New("forced install failure")
		}
		return os.Rename(oldpath, newpath)
	}
	var buf strings.Builder
	if code := upgradeCmd(&buf, nil, deps); code != 2 {
		t.Fatalf("failed apply exit = %d, want 2", code)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "old" {
		t.Fatalf("binary after failed upgrade = %q, %v; want the original restored at %s", got, err, exe)
	}
}

func TestUpgradeChecksumMismatchKeepsBinary(t *testing.T) {
	withVersion(t, "v0.1.0")
	dir := t.TempDir()
	exe := filepath.Join(dir, "botfile")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("expected"))
	m[releaseDL+"/v0.2.0/botfile-linux-amd64"] = []byte("tampered")
	var buf strings.Builder
	if code := upgradeCmd(&buf, []string{"--format", "json"}, testDeps(m, exe)); code != 2 {
		t.Fatalf("mismatch exit = %d, want 2", code)
	}
	var r upgradeReport
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		t.Fatalf("mismatch json: %v\n%s", err, buf.String())
	}
	if r.Outcome != "failed" || r.ExitCode != 2 || !strings.Contains(r.Detail, "checksum mismatch") {
		t.Errorf("report = %+v, want a failed checksum-mismatch run", r)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Errorf("binary after failed upgrade = %q, want untouched old", got)
	}
}

func TestUpgradeMissingAssetChecksum(t *testing.T) {
	withVersion(t, "v0.1.0")
	m := fakeRelease("v0.2.0", "botfile-linux-amd64", []byte("new"))
	deps := testDeps(m, "/nonexistent")
	deps.goarch = "riscv64" // no such asset in the manifest
	var buf strings.Builder
	if code := upgradeCmd(&buf, nil, deps); code != 2 {
		t.Fatalf("missing-asset exit = %d, want 2", code)
	}
}

func TestUpgradeNetworkFailureJSON(t *testing.T) {
	withVersion(t, "v0.1.0")
	deps := upgradeDeps{
		fetch:   func(string, int64) ([]byte, error) { return nil, errors.New("no route to host") },
		exePath: func() (string, error) { return "/nonexistent", nil },
		rename:  os.Rename,
		goos:    "linux", goarch: "amd64",
	}
	var buf strings.Builder
	if code := upgradeCmd(&buf, []string{"--check", "--format", "json"}, deps); code != 2 {
		t.Fatalf("network failure exit = %d, want 2", code)
	}
	var r upgradeReport
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		t.Fatalf("failure json: %v\n%s", err, buf.String())
	}
	if r.Outcome != "failed" || r.ExitCode != 2 || !strings.Contains(r.Detail, "no route to host") {
		t.Errorf("report = %+v, want a failed network run", r)
	}
}

func TestUpgradeUsageErrors(t *testing.T) {
	withVersion(t, "v0.1.0")
	var buf strings.Builder
	deps := testDeps(map[string][]byte{}, "/nonexistent")
	if code := upgradeCmd(&buf, []string{"--bogus"}, deps); code != 2 {
		t.Errorf("bogus flag exit = %d, want 2", code)
	}
	if code := upgradeCmd(&buf, []string{"--format", "yaml"}, deps); code != 2 {
		t.Errorf("bad format exit = %d, want 2", code)
	}
}

// TestHTTPFetchEnforcesLimit pins the byte cap at the real HTTP boundary: a
// body at the limit passes, one byte over is an error.
func TestHTTPFetchEnforcesLimit(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	if got, err := httpFetch(srv.URL, 64); err != nil || len(got) != 64 {
		t.Errorf("at-limit fetch = %d bytes, %v; want 64, nil", len(got), err)
	}
	if _, err := httpFetch(srv.URL, 63); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("over-limit fetch error = %v, want a limit error", err)
	}
}

func TestReleaseCompare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		current, latest      string
		upToDate, comparable bool
	}{
		{"v0.1.0", "v0.1.0", true, true},
		{"v0.1.0", "v0.2.0", false, true},
		{"v0.2.0", "v0.1.9", true, true},
		{"v1.0.0", "v0.9.9", true, true},
		{"dev", "v0.2.0", false, false},
		{"v0.1", "v0.2.0", false, false},
		{"0.1.0", "v0.2.0", false, false},
	}
	for _, c := range cases {
		upToDate, comparable := releaseCompare(c.current, c.latest)
		if upToDate != c.upToDate || comparable != c.comparable {
			t.Errorf("releaseCompare(%q, %q) = %v,%v, want %v,%v",
				c.current, c.latest, upToDate, comparable, c.upToDate, c.comparable)
		}
	}
}
