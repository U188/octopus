package update

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallUpdateCompanionsAndRollback(t *testing.T) {
	baseDir := t.TempDir()
	stageDir := filepath.Join(baseDir, "stage")
	execPath := filepath.Join(baseDir, "octopus")
	for _, entry := range []struct{ relative, content string }{
		{"bin/sing-box", "new binary"}, {"licenses/sing-box-LICENSE", "new license"},
	} {
		path := filepath.Join(stageDir, entry.relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(entry.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldBinary := filepath.Join(baseDir, "bin", "sing-box")
	if err := os.MkdirAll(filepath.Dir(oldBinary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBinary, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	rollback, err := installUpdateCompanions(stageDir, execPath)
	if err != nil {
		t.Fatalf("install companion files: %v", err)
	}
	if data, err := os.ReadFile(oldBinary); err != nil || string(data) != "new binary" {
		t.Fatalf("new binary not installed: %q, %v", data, err)
	}
	if info, err := os.Stat(oldBinary); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("installed binary mode: %v, %v", info, err)
	}
	if err := rollback(); err != nil {
		t.Fatalf("restore old companion files: %v", err)
	}
	if data, err := os.ReadFile(oldBinary); err != nil || string(data) != "old binary" {
		t.Fatalf("old binary was not restored: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "licenses", "sing-box-LICENSE")); !os.IsNotExist(err) {
		t.Fatalf("new license remained after rollback: %v", err)
	}
}

func TestInstallUpdateCompanionsRejectsIncompleteArchive(t *testing.T) {
	baseDir := t.TempDir()
	stageDir := filepath.Join(baseDir, "stage")
	if err := os.MkdirAll(filepath.Join(stageDir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "bin", "sing-box"), []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := installUpdateCompanions(stageDir, filepath.Join(baseDir, "octopus")); err == nil {
		t.Fatal("incomplete release archive was accepted")
	}
	if _, err := os.Stat(filepath.Join(baseDir, "bin", "sing-box")); !os.IsNotExist(err) {
		t.Fatalf("partial update installed a binary: %v", err)
	}
}

func TestInstallUpdateCompanionsRestoresBinaryOnLicenseInstallFailure(t *testing.T) {
	baseDir := t.TempDir()
	stageDir := filepath.Join(baseDir, "stage")
	for _, relative := range []string{"bin/sing-box", "licenses/sing-box-LICENSE"} {
		path := filepath.Join(stageDir, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("new"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldBinary := filepath.Join(baseDir, "bin", "sing-box")
	if err := os.MkdirAll(filepath.Dir(oldBinary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBinary, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "licenses"), []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := installUpdateCompanions(stageDir, filepath.Join(baseDir, "octopus")); err == nil {
		t.Fatal("license install unexpectedly succeeded")
	}
	if data, err := os.ReadFile(oldBinary); err != nil || string(data) != "old" {
		t.Fatalf("binary not restored after companion install failure: %q, %v", data, err)
	}
}

func TestBuildDownloadURL(t *testing.T) {
	const filename = "octopus-linux-x86_64.zip"
	official := updateURL + "/" + filename

	tests := []struct {
		name     string
		filename string
		custom   string
		expected string
	}{
		{
			name:     "empty uses official download URL",
			filename: filename,
			custom:   "",
			expected: official,
		},
		{
			name:     "proxy prefix prepends full official URL",
			filename: filename,
			custom:   "https://gh.llkk.cc/",
			expected: "https://gh.llkk.cc/" + official,
		},
		{
			name:     "url template inserts full official URL",
			filename: filename,
			custom:   "https://proxy.example.com/{url}",
			expected: "https://proxy.example.com/" + official,
		},
		{
			name:     "filename template inserts asset filename",
			filename: filename,
			custom:   "https://mirror.example.com/octopus/{filename}",
			expected: "https://mirror.example.com/octopus/" + filename,
		},
		{
			name:     "download base appends filename",
			filename: filename,
			custom:   "https://mirror.example.com/u188/octopus/releases/latest/download",
			expected: "https://mirror.example.com/u188/octopus/releases/latest/download/" + filename,
		},
		{
			name:     "leading slash filename is normalized",
			filename: "/" + filename,
			custom:   "https://gh.llkk.cc/",
			expected: "https://gh.llkk.cc/" + official,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDownloadURL(tt.filename, tt.custom)
			if got != tt.expected {
				t.Fatalf("buildDownloadURL() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestVerifyUpdateArchive(t *testing.T) {
	data := []byte("trusted archive")
	sum := sha256.Sum256(data)
	manifest := []byte(fmt.Sprintf("%x  %s\n", sum, "octopus-linux-x86_64.zip"))
	if err := verifyUpdateArchive(data, manifest, "octopus-linux-x86_64.zip"); err != nil {
		t.Fatalf("verifyUpdateArchive failed: %v", err)
	}
	if err := verifyUpdateArchive([]byte("tampered"), manifest, "octopus-linux-x86_64.zip"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if _, err := expectedSHA256(manifest, "missing.zip"); err == nil {
		t.Fatal("expected missing checksum to fail")
	}
}

func TestCopyUpdateFileWithLimitUsesActualBytes(t *testing.T) {
	var out bytes.Buffer
	n, err := copyUpdateFileWithLimit(&out, bytes.NewReader([]byte("12345")), 4)
	if err == nil {
		t.Fatal("expected actual extracted bytes above limit to fail")
	}
	if n != 5 {
		t.Fatalf("copied bytes = %d, want 5", n)
	}

	out.Reset()
	n, err = copyUpdateFileWithLimit(&out, bytes.NewReader([]byte("1234")), 4)
	if err != nil || n != 4 {
		t.Fatalf("expected exact limit to pass, n=%d err=%v", n, err)
	}
}

func TestShouldAttachGitHubToken(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "github.com", want: true},
		{host: "api.github.com", want: true},
		{host: "gh.llkk.cc", want: false},
		{host: "mirror.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := shouldAttachGitHubToken(tt.host); got != tt.want {
				t.Fatalf("shouldAttachGitHubToken(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestDoRequestUsesProvidedTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	if _, err := doRequest(server.URL, false, 1024, 10*time.Millisecond); err == nil {
		t.Fatal("expected request to honor the short timeout")
	}

	data, err := doRequest(server.URL, false, 1024, time.Second)
	if err != nil {
		t.Fatalf("request with sufficient timeout failed: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("response = %q, want %q", data, "ok")
	}
}
