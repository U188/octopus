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

const updateRestartDelay = time.Second

func UpdateCore() error {
	log.Infof("start update core")

	filename, err := getDownloadFilename()
	if err != nil {
		log.Warnf("update core failed: %v", err)
		return err
	}

	downloadUrl := BuildDownloadURL(filename)
	log.Infof("download url: %s", downloadUrl)
	data, err := downloadUpdateAsset(filename, maxUpdateArchiveBytes)
	if err != nil {
		log.Warnf("download failed: %v", err)
		return err
	}
	// Prefer the official manifest as the trust root. The configured accelerator
	// is only a last-resort fallback for hosts that cannot reach GitHub directly.
	checksumURL := updateURL + "/" + updateChecksumFilename
	checksumData, officialChecksumErr := doRequestWithFallback(checksumURL, maxUpdateChecksumBytes)
	if officialChecksumErr != nil {
		acceleratedChecksumURL := BuildDownloadURL(updateChecksumFilename)
		if acceleratedChecksumURL == checksumURL {
			return fmt.Errorf("download official checksum manifest: %w", officialChecksumErr)
		}
		log.Warnf("official checksum manifest unavailable; falling back to configured update accelerator: %v", officialChecksumErr)
		checksumData, err = doRequestWithFallback(acceleratedChecksumURL, maxUpdateChecksumBytes)
		if err != nil {
			return fmt.Errorf("download checksum manifest: official request failed: %v; accelerated request failed: %w", officialChecksumErr, err)
		}
	}
	if err := verifyUpdateArchive(data, checksumData, filename); err != nil {
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return err
	}

	stageDir, err := os.MkdirTemp(filepath.Dir(execPath), ".octopus-update-*")
	if err != nil {
		return fmt.Errorf("create update staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := unzip(data, stageDir); err != nil {
		log.Warnf("unzip failed: %v", err)
		return err
	}
	stagedBinary, err := findStagedBinary(stageDir, execPath)
	if err != nil {
		return err
	}
	if err := installStagedBinary(stagedBinary, execPath); err != nil {
		return err
	}

	log.Infof("update core success")
	go func() {
		time.Sleep(updateRestartDelay)
		restartExecutable(execPath)
	}()
	return nil
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
