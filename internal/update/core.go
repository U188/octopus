package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/U188/octopus/internal/utils/log"
	"github.com/U188/octopus/internal/utils/shutdown"
)

var updateRestartDelay = time.Second

// UpdateCore performs a synchronous self-update: download, verify, install, then
// restart shortly after. Retained for blocking callers and tests. Interactive
// callers should prefer StartUpdate (see status.go), which runs the same work
// asynchronously so the HTTP request does not stay open for the whole download.
func UpdateCore() error {
	execPath, err := runUpdateCore()
	if err != nil {
		return err
	}
	log.Infof("update core success")
	go func() {
		time.Sleep(updateRestartDelay)
		restartExecutable(execPath)
	}()
	return nil
}

// runUpdateCore downloads, verifies, and installs the new binary, returning the
// executable path to restart on success. It does NOT restart the process.
func runUpdateCore() (string, error) {
	log.Infof("start update core")

	filename, err := getDownloadFilename()
	if err != nil {
		log.Warnf("update core failed: %v", err)
		return "", err
	}

	downloadUrl := BuildDownloadURL(filename)
	log.Infof("download url: %s", downloadUrl)
	data, err := downloadUpdateAsset(filename, maxUpdateArchiveBytes)
	if err != nil {
		log.Warnf("download failed: %v", err)
		return "", err
	}
	// Prefer the official manifest as the trust root. The configured accelerator
	// is only a last-resort fallback for hosts that cannot reach GitHub directly.
	checksumURL := updateURL + "/" + updateChecksumFilename
	checksumData, officialChecksumErr := doRequestWithFallback(checksumURL, maxUpdateChecksumBytes)
	if officialChecksumErr != nil {
		acceleratedChecksumURL := BuildDownloadURL(updateChecksumFilename)
		if acceleratedChecksumURL == checksumURL {
			return "", fmt.Errorf("download official checksum manifest: %w", officialChecksumErr)
		}
		log.Warnf("official checksum manifest unavailable; falling back to configured update accelerator: %v", officialChecksumErr)
		checksumData, err = doRequestWithFallback(acceleratedChecksumURL, maxUpdateChecksumBytes)
		if err != nil {
			return "", fmt.Errorf("download checksum manifest: official request failed: %v; accelerated request failed: %w", officialChecksumErr, err)
		}
	}
	if err := verifyUpdateArchive(data, checksumData, filename); err != nil {
		return "", err
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return "", err
	}

	stageDir, err := os.MkdirTemp(filepath.Dir(execPath), ".octopus-update-*")
	if err != nil {
		return "", fmt.Errorf("create update staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := unzip(data, stageDir); err != nil {
		log.Warnf("unzip failed: %v", err)
		return "", err
	}
	stagedBinary, err := findStagedBinary(stageDir, execPath)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("automatic self-update on Windows is not supported; use the release installer")
	}
	rollbackCompanions, err := installUpdateCompanions(stageDir, execPath)
	if err != nil {
		return "", err
	}
	if err := installStagedBinary(stagedBinary, execPath); err != nil {
		if rollbackErr := rollbackCompanions(); rollbackErr != nil {
			return "", fmt.Errorf("replace executable: %w; restore sing-box files: %v", err, rollbackErr)
		}
		return "", err
	}

	return execPath, nil
}

// Install companion files before replacing Octopus so a failed main-binary
// replacement can still restore the previous working runtime.
func installUpdateCompanions(stageDir, execPath string) (func() error, error) {
	baseDir := filepath.Dir(execPath)
	files := []struct {
		staged string
		target string
		mode   os.FileMode
	}{
		{filepath.Join(stageDir, "bin", "sing-box"), filepath.Join(baseDir, "bin", "sing-box"), 0755},
		{filepath.Join(stageDir, "licenses", "sing-box-LICENSE"), filepath.Join(baseDir, "licenses", "sing-box-LICENSE"), 0644},
	}
	for _, file := range files {
		info, err := os.Stat(file.staged)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("update archive missing companion file %s", file.staged)
		}
	}
	type installedFile struct {
		target string
		backup string
	}
	installed := make([]installedFile, 0, len(files))
	rollback := func() error {
		var restoreErr error
		for i := len(installed) - 1; i >= 0; i-- {
			file := installed[i]
			if err := os.Remove(file.target); err != nil && !os.IsNotExist(err) {
				restoreErr = fmt.Errorf("remove new %s: %w", file.target, err)
				continue
			}
			if file.backup != "" {
				if err := os.Rename(file.backup, file.target); err != nil {
					restoreErr = fmt.Errorf("restore %s: %w", file.target, err)
				}
			}
		}
		return restoreErr
	}
	for i, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.target), 0755); err != nil {
			_ = rollback()
			return nil, err
		}
		if err := os.Chmod(file.staged, file.mode); err != nil {
			_ = rollback()
			return nil, err
		}
		backup := ""
		if _, err := os.Lstat(file.target); err == nil {
			backup = filepath.Join(stageDir, fmt.Sprintf(".previous-companion-%d", i))
			if err := os.Rename(file.target, backup); err != nil {
				_ = rollback()
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			_ = rollback()
			return nil, err
		}
		if err := os.Rename(file.staged, file.target); err != nil {
			if backup != "" {
				_ = os.Rename(backup, file.target)
			}
			_ = rollback()
			return nil, err
		}
		installed = append(installed, installedFile{target: file.target, backup: backup})
	}
	return rollback, nil
}

func downloadUpdateAsset(filename string, maxBytes int64) ([]byte, error) {
	officialURL := updateURL + "/" + strings.TrimLeft(filename, "/")
	downloadURL := BuildDownloadURL(filename)
	data, err := doRequestWithFallbackTimeout(downloadURL, maxBytes, updateArchiveTimeout)
	if err == nil || downloadURL == officialURL {
		return data, err
	}
	log.Warnf("configured update accelerator failed; falling back to official download: %v", err)
	officialData, officialErr := doRequestWithFallbackTimeout(officialURL, maxBytes, updateArchiveTimeout)
	if officialErr != nil {
		return nil, fmt.Errorf("accelerated download failed: %v; official download failed: %w", err, officialErr)
	}
	return officialData, nil
}

func findStagedBinary(stageDir, execPath string) (string, error) {
	expectedName := filepath.Base(execPath)
	fallbackName := "octopus"
	if runtime.GOOS == "windows" {
		fallbackName += ".exe"
	}
	candidates := []string{
		filepath.Join(stageDir, expectedName),
		filepath.Join(stageDir, fallbackName),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("update archive does not contain executable %q", expectedName)
}

func installStagedBinary(stagedBinary, execPath string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("automatic self-update on Windows is not supported; use the release installer")
	}
	if err := os.Chmod(stagedBinary, 0755); err != nil {
		return fmt.Errorf("set staged executable mode: %w", err)
	}
	if err := os.Rename(stagedBinary, execPath); err != nil {
		return fmt.Errorf("atomically replace executable: %w", err)
	}
	return nil
}

func RestartCore(delay time.Duration) error {
	execPath, err := os.Executable()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return err
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		restartExecutable(execPath)
	}()
	return nil
}

func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "386":
			return "octopus-windows-x86.zip", nil
		case "amd64":
			return "octopus-windows-x86_64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-darwin-x86_64.zip", nil
		case "arm64":
			return "octopus-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-linux-x86.zip", nil
		case "amd64":
			return "octopus-linux-x86_64.zip", nil
		case "arm":
			return "octopus-linux-armv7.zip", nil
		case "arm64":
			return "octopus-linux-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
	}
}
