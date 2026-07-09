package update

import (
	"archive/zip"
	"bytes"
	"context"
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
	updateUrl    = "https://github.com/u188/octopus/releases/latest/download"
	updateApiUrl = "https://api.github.com/repos/u188/octopus/releases/latest"
)

type LatestInfo struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Message     string `json:"message"`
}

var github_pat = os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT")

// doRequestWithFallback performs an HTTP GET request, first without proxy, then with proxy if failed.
func doRequestWithFallback(url string) ([]byte, error) {
	data, err := doRequest(url, false)
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
	proxyData, proxyErr := doRequest(url, true)
	if proxyErr != nil {
		return nil, fmt.Errorf("direct request failed: %v; proxy request failed: %w", err, proxyErr)
	}
	return proxyData, nil
}

func doRequest(url string, useProxy bool) ([]byte, error) {
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

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Debugf("read body failed: %v", err)
		return nil, err
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
	body, err := doRequestWithFallback(updateApiUrl)
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
	official := updateUrl + "/" + cleanFilename
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

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if err := extractFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return err
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return err
	}
	defer rc.Close()

	if _, err = io.Copy(outFile, rc); err != nil {
		log.Debugf("copy failed: %v", err)
		return err
	}
	return nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
