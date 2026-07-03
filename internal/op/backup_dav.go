package op

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var webDAVHTTPClient = &http.Client{Timeout: 30 * time.Minute}

const webDAVAutoBackupPrefix = "octopus-auto-db-"
const webDAVMaxSingleUploadSize int64 = 80 * 1024 * 1024

type webDAVBackupManifest struct {
	Version            int                `json:"version"`
	Kind               string             `json:"kind"`
	OriginalFilename   string             `json:"original_filename"`
	CompressedFilename string             `json:"compressed_filename"`
	Compression        string             `json:"compression"`
	Size               int64              `json:"size"`
	CompressedSize     int64              `json:"compressed_size"`
	Parts              []webDAVBackupPart `json:"parts"`
}

type webDAVBackupPart struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
}

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
		if isWebDAVPartFilename(name) {
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

func WebDAVValidateCredentials(ctx context.Context, cred model.WebDAVCredentials) error {
	if err := validateWebDAVCredentials(cred); err != nil {
		return err
	}
	reqBody := strings.NewReader(`<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/></D:prop></D:propfind>`)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", strings.TrimRight(cred.URL, "/")+"/", reqBody)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cred.Username, cred.Password)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")

	res, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("webdav validate: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return webDAVStatusError("webdav validate", res)
	}
	return nil
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
	if !isPlainSQLiteBackupFilename(filename) {
		return nil, fmt.Errorf("webdav backup filename must end with .db, .sqlite, .sqlite3, or .db3")
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
	if err := stripRelayLogsFromSQLiteBackup(tmpPath); err != nil {
		return nil, err
	}
	if err := db.ValidateSQLiteDatabaseFile(tmpPath); err != nil {
		return nil, fmt.Errorf("validate database backup: %w", err)
	}
	stat, err := os.Stat(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("stat database backup: %w", err)
	}
	if stat.Size() <= webDAVMaxSingleUploadSize {
		if err := webDAVPutFile(ctx, req.WebDAVCredentials, filename, tmpPath, stat.Size()); err != nil {
			return nil, err
		}
		return &model.WebDAVBackupResult{Filename: filename, Size: stat.Size()}, nil
	}
	uploadedFilename, err := webDAVUploadCompressedBackup(ctx, req.WebDAVCredentials, filename, tmpPath, stat.Size())
	if err != nil {
		return nil, err
	}
	return &model.WebDAVBackupResult{Filename: uploadedFilename, Size: stat.Size()}, nil
}

func stripRelayLogsFromSQLiteBackup(dbPath string) error {
	conn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("open lightweight database backup: %w", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return fmt.Errorf("open lightweight database backup handle: %w", err)
	}
	defer sqlDB.Close()

	var tableName string
	if err := conn.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'relay_logs' LIMIT 1").Scan(&tableName).Error; err != nil {
		return fmt.Errorf("inspect relay_logs in database backup: %w", err)
	}
	if tableName != "relay_logs" {
		return nil
	}
	if err := conn.Exec("DELETE FROM relay_logs").Error; err != nil {
		return fmt.Errorf("strip relay_logs from database backup: %w", err)
	}
	var sequenceTable string
	if err := conn.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence' LIMIT 1").Scan(&sequenceTable).Error; err != nil {
		return fmt.Errorf("inspect sqlite_sequence in database backup: %w", err)
	}
	if sequenceTable == "sqlite_sequence" {
		if err := conn.Exec("DELETE FROM sqlite_sequence WHERE name = 'relay_logs'").Error; err != nil {
			return fmt.Errorf("reset relay_logs sequence in database backup: %w", err)
		}
	}
	if err := conn.Exec("VACUUM").Error; err != nil {
		return fmt.Errorf("compact lightweight database backup: %w", err)
	}
	return nil
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
		if err := WebDAVDeleteBackupSet(ctx, cred, file.Name); err != nil {
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
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	size, err := webDAVDownloadBackupToSQLite(ctx, req.WebDAVCredentials, filename, tmpPath)
	if err != nil {
		return nil, err
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

func WebDAVDeleteBackupSet(ctx context.Context, cred model.WebDAVCredentials, filename string) error {
	if isWebDAVManifestFilename(filename) {
		manifest, err := webDAVDownloadManifest(ctx, cred, filename)
		if err == nil {
			for _, part := range manifest.Parts {
				if err := validateWebDAVFilename(part.Name); err == nil {
					_ = WebDAVDeleteBackup(ctx, cred, part.Name)
				}
			}
		}
	}
	return WebDAVDeleteBackup(ctx, cred, filename)
}

func webDAVUploadCompressedBackup(ctx context.Context, cred model.WebDAVCredentials, filename, sourcePath string, sourceSize int64) (string, error) {
	gzPath := sourcePath + ".gz"
	defer os.Remove(gzPath)
	if err := gzipFile(sourcePath, gzPath); err != nil {
		return "", err
	}
	gzStat, err := os.Stat(gzPath)
	if err != nil {
		return "", fmt.Errorf("stat compressed database backup: %w", err)
	}
	gzFilename := filename + ".gz"
	if gzStat.Size() <= webDAVMaxSingleUploadSize {
		if err := webDAVPutFile(ctx, cred, gzFilename, gzPath, gzStat.Size()); err != nil {
			return "", err
		}
		return gzFilename, nil
	}
	return webDAVUploadSplitBackup(ctx, cred, filename, gzFilename, gzPath, sourceSize, gzStat.Size())
}

func webDAVUploadSplitBackup(ctx context.Context, cred model.WebDAVCredentials, originalFilename, gzFilename, gzPath string, sourceSize, compressedSize int64) (string, error) {
	f, err := os.Open(gzPath)
	if err != nil {
		return "", fmt.Errorf("open compressed database backup: %w", err)
	}
	defer f.Close()

	parts := make([]webDAVBackupPart, 0, int((compressedSize/webDAVMaxSingleUploadSize)+1))
	for offset, index := int64(0), 1; offset < compressedSize; index++ {
		size := webDAVMaxSingleUploadSize
		if remaining := compressedSize - offset; remaining < size {
			size = remaining
		}
		partName := fmt.Sprintf("%s.part%04d", gzFilename, index)
		reader := io.NewSectionReader(f, offset, size)
		if err := webDAVPutReader(ctx, cred, partName, reader, size); err != nil {
			return "", err
		}
		parts = append(parts, webDAVBackupPart{Index: index, Name: partName, Size: size})
		offset += size
	}

	manifest := webDAVBackupManifest{
		Version:            1,
		Kind:               "sqlite-gzip-split",
		OriginalFilename:   originalFilename,
		CompressedFilename: gzFilename,
		Compression:        "gzip",
		Size:               sourceSize,
		CompressedSize:     compressedSize,
		Parts:              parts,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode backup manifest: %w", err)
	}
	manifestName := gzFilename + ".manifest.json"
	if err := webDAVPutReader(ctx, cred, manifestName, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", err
	}
	return manifestName, nil
}

func webDAVDownloadBackupToSQLite(ctx context.Context, cred model.WebDAVCredentials, filename, destPath string) (int64, error) {
	switch {
	case isWebDAVManifestFilename(filename):
		return webDAVDownloadSplitBackupToSQLite(ctx, cred, filename, destPath)
	case isWebDAVGzipFilename(filename):
		gzPath := destPath + ".gz"
		defer os.Remove(gzPath)
		gz, err := os.Create(gzPath)
		if err != nil {
			return 0, fmt.Errorf("create compressed restore temp file: %w", err)
		}
		size, err := webDAVDownloadFile(ctx, cred, filename, gz)
		closeErr := gz.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, fmt.Errorf("close compressed restore temp file: %w", closeErr)
		}
		if err := gunzipFile(gzPath, destPath); err != nil {
			return 0, err
		}
		return size, nil
	default:
		tmp, err := os.Create(destPath)
		if err != nil {
			return 0, fmt.Errorf("create database restore temp file: %w", err)
		}
		size, err := webDAVDownloadFile(ctx, cred, filename, tmp)
		closeErr := tmp.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, fmt.Errorf("close database restore temp file: %w", closeErr)
		}
		return size, nil
	}
}

func webDAVDownloadSplitBackupToSQLite(ctx context.Context, cred model.WebDAVCredentials, manifestName, destPath string) (int64, error) {
	manifest, err := webDAVDownloadManifest(ctx, cred, manifestName)
	if err != nil {
		return 0, err
	}
	gzPath := destPath + ".gz"
	defer os.Remove(gzPath)
	gz, err := os.Create(gzPath)
	if err != nil {
		return 0, fmt.Errorf("create compressed restore temp file: %w", err)
	}
	for _, part := range manifest.Parts {
		if part.Size <= 0 {
			_ = gz.Close()
			return 0, fmt.Errorf("backup manifest contains invalid part size")
		}
		if err := validateWebDAVFilename(part.Name); err != nil {
			_ = gz.Close()
			return 0, err
		}
		if _, err := webDAVDownloadFile(ctx, cred, part.Name, gz); err != nil {
			_ = gz.Close()
			return 0, err
		}
	}
	if err := gz.Close(); err != nil {
		return 0, fmt.Errorf("close compressed restore temp file: %w", err)
	}
	if err := gunzipFile(gzPath, destPath); err != nil {
		return 0, err
	}
	return manifest.Size, nil
}

func webDAVDownloadManifest(ctx context.Context, cred model.WebDAVCredentials, filename string) (*webDAVBackupManifest, error) {
	var buf bytes.Buffer
	if _, err := webDAVDownloadFile(ctx, cred, filename, &buf); err != nil {
		return nil, err
	}
	var manifest webDAVBackupManifest
	if err := json.Unmarshal(buf.Bytes(), &manifest); err != nil {
		return nil, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.Version != 1 || manifest.Kind != "sqlite-gzip-split" || len(manifest.Parts) == 0 {
		return nil, fmt.Errorf("backup manifest is invalid")
	}
	return &manifest, nil
}

func gzipFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open database backup: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create compressed database backup: %w", err)
	}
	gz, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("create gzip writer: %w", err)
	}
	_, copyErr := io.Copy(gz, in)
	closeGzipErr := gz.Close()
	closeOutErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("compress database backup: %w", copyErr)
	}
	if closeGzipErr != nil {
		return fmt.Errorf("close compressed database backup: %w", closeGzipErr)
	}
	if closeOutErr != nil {
		return fmt.Errorf("close compressed database backup file: %w", closeOutErr)
	}
	return nil
}

func gunzipFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open compressed database restore: %w", err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("open gzip database restore: %w", err)
	}
	defer gz.Close()
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create database restore: %w", err)
	}
	_, copyErr := io.Copy(out, gz)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("decompress database restore: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close database restore: %w", closeErr)
	}
	return db.ValidateSQLiteDatabaseFile(dest)
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
		return fmt.Errorf("webdav backup filename must end with a supported sqlite backup extension")
	}
	return nil
}

func isSQLiteBackupFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return isPlainSQLiteBackupFilename(name) ||
		isWebDAVGzipFilename(name) ||
		isWebDAVManifestFilename(name) ||
		isWebDAVPartFilename(name)
}

func isPlainSQLiteBackupFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return strings.HasSuffix(name, ".db") ||
		strings.HasSuffix(name, ".sqlite") ||
		strings.HasSuffix(name, ".sqlite3") ||
		strings.HasSuffix(name, ".db3")
}

func isWebDAVGzipFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return strings.HasSuffix(name, ".db.gz") ||
		strings.HasSuffix(name, ".sqlite.gz") ||
		strings.HasSuffix(name, ".sqlite3.gz") ||
		strings.HasSuffix(name, ".db3.gz")
}

func isWebDAVManifestFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return strings.HasSuffix(name, ".db.gz.manifest.json") ||
		strings.HasSuffix(name, ".sqlite.gz.manifest.json") ||
		strings.HasSuffix(name, ".sqlite3.gz.manifest.json") ||
		strings.HasSuffix(name, ".db3.gz.manifest.json")
}

func isWebDAVPartFilename(filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	return strings.Contains(name, ".gz.part") &&
		(strings.HasSuffix(name, ".db.gz.part0001") ||
			strings.Contains(name, ".db.gz.part") ||
			strings.Contains(name, ".sqlite.gz.part") ||
			strings.Contains(name, ".sqlite3.gz.part") ||
			strings.Contains(name, ".db3.gz.part"))
}

func webDAVPutFile(ctx context.Context, cred model.WebDAVCredentials, filename, filePath string, size int64) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open database backup: %w", err)
	}
	defer f.Close()

	return webDAVPutReader(ctx, cred, filename, f, size)
}

func webDAVPutReader(ctx context.Context, cred model.WebDAVCredentials, filename string, r io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, webDAVFileURL(cred.URL, filename), r)
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
