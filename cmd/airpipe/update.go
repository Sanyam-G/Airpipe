package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func cmdUpdate() error {
	banner("update")

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	url := fmt.Sprintf("https://github.com/Sanyam-G/Airpipe/releases/latest/download/airpipe-%s-%s%s", goos, goarch, ext)

	fmt.Printf("  Current: %s%s%s\n", colorDim, buildVersion, colorReset)
	fmt.Printf("  Downloading latest for %s/%s...\n", goos, goarch)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	binary, err := io.ReadAll(resp.Body)
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

	// Write new binary to /tmp
	tmpPath := filepath.Join(os.TempDir(), "airpipe-update"+ext)
	if err := os.WriteFile(tmpPath, binary, 0755); err != nil {
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
		cmd := exec.Command("sudo", "sh", "-c",
			fmt.Sprintf("rm -f %s && mv %s %s", execPath, tmpPath, execPath))
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

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}
