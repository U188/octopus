package update

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

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
