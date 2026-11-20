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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func cmdUpdate() error {
	banner("update")

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	asset := fmt.Sprintf("airpipe-%s-%s%s", goos, goarch, ext)

	fmt.Printf("  Current: %s%s%s\n", colorDim, buildVersion, colorReset)
	fmt.Printf("  Downloading latest for %s/%s...\n", goos, goarch)

	binary, err := downloadRelease(latestReleaseAPI, asset, buildVersion)
	if errors.Is(err, errUpToDate) {
		fmt.Printf("  %s✓ Already on the latest release%s\n\n", colorGreen, colorReset)
		return nil
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Find current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not find current binary: %w", err)
	}
	// Resolve symlinks
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("could not resolve path: %w", err)
	}

	// Unpredictable name, created exclusively: another local user can't
	// pre-place a file or symlink at the path.
	tmp, err := os.CreateTemp("", "airpipe-update-*"+ext)
	if err != nil {
		return fmt.Errorf("write to temp failed: %w", err)
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(binary)
	if err == nil {
		err = tmp.Chmod(0755)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write to temp failed: %w", err)
	}

	// Windows can't remove a running exe but can rename it.
	if goos == "windows" {
		old := execPath + ".old"
		os.Remove(old)
		if err := os.Rename(execPath, old); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("move old binary aside failed: %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			if err := copyFile(tmpPath, execPath); err != nil {
				os.Rename(old, execPath)
				os.Remove(tmpPath)
				return fmt.Errorf("move failed: %w", err)
			}
			os.Remove(tmpPath)
		}
		fmt.Printf("  %s✓ Updated %s%s (%s)\n", colorGreen, execPath, colorReset, fmtBytes(int64(len(binary))))
		fmt.Printf("  %sDelete %s once no airpipe is running.%s\n\n", colorDim, old, colorReset)
		return nil
	}

	// Replace the running binary: remove old, then move new in.
	// Can't overwrite a running binary on Linux, but removing + renaming works.
	// Try without sudo first, then escalate.
	if err := os.Remove(execPath); err == nil {
		if err := os.Rename(tmpPath, execPath); err != nil {
			// Cross-filesystem, use copy
			if err := copyFile(tmpPath, execPath); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("move failed: %w", err)
			}
		}
		os.Remove(tmpPath)
	} else {
		// Need sudo: remove old binary, move new one in
		fmt.Printf("  Need sudo to update %s\n", execPath)
		cmd := exec.Command("sudo", "sh", "-c", `rm -f "$1" && mv "$2" "$1"`, "sh", execPath, tmpPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("sudo update failed: %w", err)
		}
	}

	fmt.Printf("  %s✓ Updated %s%s (%s)\n\n", colorGreen, execPath, colorReset, fmtBytes(int64(len(binary))))
	return nil
}

const latestReleaseAPI = "https://api.github.com/repos/Sanyam-G/Airpipe/releases/latest"

var errUpToDate = errors.New("already up to date")

// downloadRelease fetches the named asset of the release described at apiURL
// and checks it against the SHA-256 digest GitHub records for the asset.
// It returns errUpToDate when that release is the current version.
func downloadRelease(apiURL, asset, current string) ([]byte, error) {
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	meta, err := httpGet(apiURL, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(meta, &release); err != nil {
		return nil, fmt.Errorf("release info: %w", err)
	}
	if release.Tag != "" && release.Tag == current {
		return nil, errUpToDate
	}
	for _, a := range release.Assets {
		if a.Name != asset {
			continue
		}
		want, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok {
			return nil, fmt.Errorf("release has no checksum for %s", asset)
		}
		binary, err := httpGet(a.URL, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(binary)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
		}
		return binary, nil
	}
	return nil, fmt.Errorf("latest release has no %s", asset)
}

func httpGet(url string, timeout time.Duration) ([]byte, error) {
	resp, err := (&http.Client{Timeout: timeout}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}
