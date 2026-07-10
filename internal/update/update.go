package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/U188/octopus/internal/client"
	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

const (
	updateURL                     = "https://github.com/u188/octopus/releases/latest/download"
	updateAPIURL                  = "https://api.github.com/repos/u188/octopus/releases/latest"
	updateChecksumFilename        = "sha256sums.txt"
	maxUpdateMetadataBytes  int64 = 4 << 20
	maxUpdateArchiveBytes   int64 = 512 << 20
	maxUpdateChecksumBytes  int64 = 1 << 20
	maxUpdateExtractedBytes int64 = 1024 << 20
)

type LatestInfo struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Message     string `json:"message"`
}

var github_pat = os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT")

// doRequestWithFallback performs an HTTP GET request, first without proxy, then with proxy if failed.
func doRequestWithFallback(url string, maxBytes int64) ([]byte, error) {
	data, err := doRequest(url, false, maxBytes)
	if err == nil {
		return data, nil
	}

	proxyURL, proxyCfgErr := op.SettingGetString(model.SettingKeyProxyURL)
	if proxyCfgErr != nil {
		return nil, fmt.Errorf("direct request failed: %w; read proxy setting failed: %v", err, proxyCfgErr)
	}
	if strings.TrimSpace(proxyURL) == "" {
		return nil, err
	}

	log.Warnf("direct request failed, trying with proxy: %v", err)
	proxyData, proxyErr := doRequest(url, true, maxBytes)
	if proxyErr != nil {
		return nil, fmt.Errorf("direct request failed: %v; proxy request failed: %w", err, proxyErr)
	}
	return proxyData, nil
}

func doRequest(url string, useProxy bool, maxBytes int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hc, err := client.GetHTTPClientSystemProxy(useProxy)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Debugf("new request failed: %v", err)
		return nil, err
	}

	if github_pat != "" && shouldAttachGitHubToken(req.URL.Hostname()) {
		req.Header.Set("Authorization", "Bearer "+github_pat)
	}

	resp, err := hc.Do(req)
	if err != nil {
		log.Debugf("request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds maximum size of %d bytes", url, maxBytes)
	}
	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		log.Debugf("read body failed: %v", err)
		return nil, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds maximum size of %d bytes", url, maxBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("request %s failed with HTTP %d: %s", url, resp.StatusCode, compactResponseSnippet(data))
	}
	return data, nil
}

func compactResponseSnippet(data []byte) string {
	const limit = 240
	text := strings.TrimSpace(string(data))
	if text == "" {
		return http.StatusText(http.StatusBadGateway)
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func GetLatestInfo() (*LatestInfo, error) {
	body, err := doRequestWithFallback(updateAPIURL, maxUpdateMetadataBytes)
	if err != nil {
		return nil, err
	}

	var latestInfo LatestInfo
	if err := json.Unmarshal(body, &latestInfo); err != nil {
		log.Debugf("unmarshal body failed: %v", err)
		return nil, err
	}
	if latestInfo.Message != "" {
		return nil, fmt.Errorf("failed to get latest info: %s", latestInfo.Message)
	}
	return &latestInfo, nil
}

func BuildDownloadURL(filename string) string {
	custom, err := op.SettingGetString(model.SettingKeyUpdateDownloadURL)
	if err != nil {
		return buildDownloadURL(filename, "")
	}
	return buildDownloadURL(filename, custom)
}

func buildDownloadURL(filename, custom string) string {
	cleanFilename := strings.TrimLeft(filename, "/")
	official := updateURL + "/" + cleanFilename
	custom = strings.TrimSpace(custom)
	if custom == "" {
		return official
	}
	if strings.Contains(custom, "{url}") {
		return strings.ReplaceAll(custom, "{url}", official)
	}
	if strings.Contains(custom, "{filename}") {
		return strings.ReplaceAll(custom, "{filename}", cleanFilename)
	}
	if strings.HasSuffix(strings.TrimRight(custom, "/"), "/download") {
		return strings.TrimRight(custom, "/") + "/" + cleanFilename
	}
	return strings.TrimRight(custom, "/") + "/" + official
}

func shouldAttachGitHubToken(host string) bool {
	return host == "github.com" || host == "api.github.com"
}

func unzip(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Debugf("new zip reader failed: %v", err)
		return err
	}

	var totalExtracted int64
	for _, f := range r.File {
		if f.UncompressedSize64 > uint64(maxUpdateExtractedBytes) ||
			totalExtracted > maxUpdateExtractedBytes-int64(f.UncompressedSize64) {
			return fmt.Errorf("update archive exceeds maximum extracted size of %d bytes", maxUpdateExtractedBytes)
		}
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		extracted, err := extractFile(f, fpath, maxUpdateExtractedBytes-totalExtracted)
		if err != nil {
			return err
		}
		totalExtracted += extracted
	}
	return nil
}

func extractFile(f *zip.File, fpath string, maxBytes int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return 0, err
	}

	mode := f.Mode().Perm() & 0777
	if mode == 0 {
		mode = 0644
	}
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return 0, err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return 0, err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return 0, err
	}
	defer rc.Close()

	n, err := copyUpdateFileWithLimit(outFile, rc, maxBytes)
	if err != nil {
		log.Debugf("copy failed: %v", err)
		return n, err
	}
	return n, nil
}

func copyUpdateFileWithLimit(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, fmt.Errorf("invalid update extraction limit")
	}
	n, err := io.Copy(dst, io.LimitReader(src, maxBytes+1))
	if err != nil {
		return n, err
	}
	if n > maxBytes {
		return n, fmt.Errorf("update archive exceeds maximum extracted size of %d bytes", maxUpdateExtractedBytes)
	}
	return n, nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}

func expectedSHA256(checksumData []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(checksumData), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name != filename {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(fields[0]))
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 checksum for %s", filename)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid SHA-256 checksum for %s: %w", filename, err)
		}
		return sum, nil
	}
	return "", fmt.Errorf("SHA-256 checksum for %s is missing", filename)
}

func verifyUpdateArchive(data, checksumData []byte, filename string) error {
	expected, err := expectedSHA256(checksumData, filename)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != expected {
		return fmt.Errorf("SHA-256 checksum mismatch for %s", filename)
	}
	return nil
}
