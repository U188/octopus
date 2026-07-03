package op

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

var webDAVHTTPClient = &http.Client{Timeout: 30 * time.Minute}

const webDAVAutoBackupPrefix = "octopus-auto-db-"

func WebDAVBackupList(ctx context.Context, cred model.WebDAVCredentials) ([]model.WebDAVBackupFile, error) {
	if err := validateWebDAVCredentials(cred); err != nil {
		return nil, err
	}
	reqBody := strings.NewReader(`<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", strings.TrimRight(cred.URL, "/")+"/", reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cred.Username, cred.Password)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")

	res, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webdav list: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, webDAVStatusError("webdav list", res)
	}

	var multi webDAVMultiStatus
	if err := xml.NewDecoder(res.Body).Decode(&multi); err != nil {
		return nil, fmt.Errorf("webdav list decode: %w", err)
	}

	files := make([]model.WebDAVBackupFile, 0, len(multi.Responses))
	for _, item := range multi.Responses {
		name, ok := webDAVResponseFilename(item.Href)
		if !ok || !isSQLiteBackupFilename(name) {
			continue
		}
		prop := item.firstOKProp()
		if prop.ResourceType.Collection != nil {
			continue
		}
		files = append(files, model.WebDAVBackupFile{
			Name:       name,
			Size:       prop.ContentLengthValue(),
			ModifiedAt: prop.LastModifiedValue(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		left := files[i].ModifiedAt
		right := files[j].ModifiedAt
		if left != nil && right != nil && !left.Equal(*right) {
			return left.After(*right)
		}
		return files[i].Name > files[j].Name
	})
	return files, nil
}

func WebDAVBackupSQLite(ctx context.Context, req model.WebDAVBackupRequest) (*model.WebDAVBackupResult, error) {
	if conf.AppConfig.Database.Type != "sqlite" {
		return nil, fmt.Errorf("webdav database backup only supports sqlite")
	}
	if err := validateWebDAVCredentials(req.WebDAVCredentials); err != nil {
		return nil, err
	}
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = "octopus-db-" + time.Now().Format("20060102150405") + ".db"
	}
	if err := validateWebDAVFilename(filename); err != nil {
		return nil, err
	}

	if err := SaveCache(); err != nil {
		return nil, fmt.Errorf("save cache before database backup: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(conf.AppConfig.Database.Path), ".octopus-dav-backup-*.db")
	if err != nil {
		return nil, fmt.Errorf("create database backup temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := db.CreateSQLiteSnapshot(ctx, tmpPath); err != nil {
		return nil, err
	}
	if err := db.ValidateSQLiteDatabaseFile(tmpPath); err != nil {
		return nil, fmt.Errorf("validate database backup: %w", err)
	}
	stat, err := os.Stat(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("stat database backup: %w", err)
	}
	if err := webDAVPutFile(ctx, req.WebDAVCredentials, filename, tmpPath, stat.Size()); err != nil {
		return nil, err
	}
	return &model.WebDAVBackupResult{Filename: filename, Size: stat.Size()}, nil
}

func WebDAVAutoBackupSQLite(ctx context.Context, cred model.WebDAVCredentials, retention int) (*model.WebDAVBackupResult, error) {
	result, err := WebDAVBackupSQLite(ctx, model.WebDAVBackupRequest{
		WebDAVCredentials: cred,
		Filename:          webDAVAutoBackupPrefix + time.Now().Format("20060102150405") + ".db",
	})
	if err != nil {
		return nil, err
	}
	if retention > 0 {
		if err := WebDAVPruneAutoBackups(ctx, cred, retention); err != nil {
			return result, fmt.Errorf("backup succeeded but prune failed: %w", err)
		}
	}
	return result, nil
}

func WebDAVPruneAutoBackups(ctx context.Context, cred model.WebDAVCredentials, keep int) error {
	if keep <= 0 {
		return nil
	}
	files, err := WebDAVBackupList(ctx, cred)
	if err != nil {
		return err
	}
	autoFiles := make([]model.WebDAVBackupFile, 0, len(files))
	for _, file := range files {
		if strings.HasPrefix(file.Name, webDAVAutoBackupPrefix) {
			autoFiles = append(autoFiles, file)
		}
	}
	if len(autoFiles) <= keep {
		return nil
	}
	for _, file := range autoFiles[keep:] {
		if err := WebDAVDeleteBackup(ctx, cred, file.Name); err != nil {
			return err
		}
	}
	return nil
}

func WebDAVRestoreSQLite(ctx context.Context, req model.WebDAVRestoreRequest) (*model.WebDAVRestoreResult, error) {
	if conf.AppConfig.Database.Type != "sqlite" {
		return nil, fmt.Errorf("webdav database restore only supports sqlite")
	}
	if err := validateWebDAVCredentials(req.WebDAVCredentials); err != nil {
		return nil, err
	}
	filename := strings.TrimSpace(req.Filename)
	if err := validateWebDAVFilename(filename); err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(conf.AppConfig.Database.Path), ".octopus-dav-restore-*.db")
	if err != nil {
		return nil, fmt.Errorf("create database restore temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	size, err := webDAVDownloadFile(ctx, req.WebDAVCredentials, filename, tmp)
	closeErr := tmp.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close database restore temp file: %w", closeErr)
	}
	if err := db.ScheduleSQLiteRestore(conf.AppConfig.Database.Path, tmpPath); err != nil {
		return nil, err
	}

	return &model.WebDAVRestoreResult{
		Filename:        filename,
		Size:            size,
		RestorePending:  true,
		RestartRequired: true,
	}, nil
}

func WebDAVDeleteBackup(ctx context.Context, cred model.WebDAVCredentials, filename string) error {
	if err := validateWebDAVCredentials(cred); err != nil {
		return err
	}
	filename = strings.TrimSpace(filename)
	if err := validateWebDAVFilename(filename); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, webDAVFileURL(cred.URL, filename), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cred.Username, cred.Password)
	res, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("webdav delete: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return webDAVStatusError("webdav delete", res)
	}
	return nil
}

func validateWebDAVCredentials(cred model.WebDAVCredentials) error {
	parsed, err := url.Parse(strings.TrimSpace(cred.URL))
	if err != nil {
		return fmt.Errorf("webdav url is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("webdav url scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("webdav url must have a host")
	}
	if strings.TrimSpace(cred.Username) == "" {
		return fmt.Errorf("webdav username is required")
	}
	if cred.Password == "" {
		return fmt.Errorf("webdav password is required")
	}
	return nil
}

func validateWebDAVFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("webdav backup filename is required")
	}
	if filename != path.Base(filename) || strings.Contains(filename, "/") || strings.Contains(filename, `\`) {
		return fmt.Errorf("webdav backup filename must not contain path separators")
	}
	if !isSQLiteBackupFilename(filename) {
		return fmt.Errorf("webdav backup filename must end with .db, .sqlite, .sqlite3, or .db3")
	}
	return nil
}

func isSQLiteBackupFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return strings.HasSuffix(name, ".db") ||
		strings.HasSuffix(name, ".sqlite") ||
		strings.HasSuffix(name, ".sqlite3") ||
		strings.HasSuffix(name, ".db3")
}

func webDAVPutFile(ctx context.Context, cred model.WebDAVCredentials, filename, filePath string, size int64) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open database backup: %w", err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, webDAVFileURL(cred.URL, filename), f)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cred.Username, cred.Password)
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	res, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("webdav upload: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return webDAVStatusError("webdav upload", res)
	}
	return nil
}

func webDAVDownloadFile(ctx context.Context, cred model.WebDAVCredentials, filename string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webDAVFileURL(cred.URL, filename), nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(cred.Username, cred.Password)
	res, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webdav download: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0, webDAVStatusError("webdav download", res)
	}
	n, err := io.Copy(w, res.Body)
	if err != nil {
		return 0, fmt.Errorf("write database restore: %w", err)
	}
	return n, nil
}

func webDAVFileURL(baseURL, filename string) string {
	parsed, _ := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + filename
	parsed.RawPath = ""
	return parsed.String()
}

func webDAVStatusError(operation string, res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	body = bytes.TrimSpace(body)
	if len(body) > 0 {
		return fmt.Errorf("%s failed: %s: %s", operation, res.Status, string(body))
	}
	return fmt.Errorf("%s failed: %s", operation, res.Status)
}

func webDAVResponseFilename(href string) (string, bool) {
	if href == "" {
		return "", false
	}
	parsed, err := url.Parse(href)
	if err == nil {
		href = parsed.Path
	}
	name, err := url.PathUnescape(path.Base(strings.TrimRight(href, "/")))
	if err != nil || name == "." || name == "/" || name == "" {
		return "", false
	}
	return name, true
}

type webDAVMultiStatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href     string           `xml:"href"`
	PropStat []webDAVPropStat `xml:"propstat"`
}

func (r webDAVResponse) firstOKProp() webDAVProp {
	for _, propStat := range r.PropStat {
		if strings.Contains(propStat.Status, " 200 ") {
			return propStat.Prop
		}
	}
	if len(r.PropStat) > 0 {
		return r.PropStat[0].Prop
	}
	return webDAVProp{}
}

type webDAVPropStat struct {
	Prop   webDAVProp `xml:"prop"`
	Status string     `xml:"status"`
}

type webDAVProp struct {
	ContentLength string             `xml:"getcontentlength"`
	LastModified  string             `xml:"getlastmodified"`
	ResourceType  webDAVResourceType `xml:"resourcetype"`
}

func (p webDAVProp) ContentLengthValue() int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(p.ContentLength), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func (p webDAVProp) LastModifiedValue() *time.Time {
	if strings.TrimSpace(p.LastModified) == "" {
		return nil
	}
	t, err := http.ParseTime(p.LastModified)
	if err != nil {
		return nil
	}
	return &t
}

type webDAVResourceType struct {
	Collection *struct{} `xml:"collection"`
}
