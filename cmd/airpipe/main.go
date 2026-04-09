package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sanyamgarg/airpipe/internal/archive"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/passphrase"
	"github.com/sanyamgarg/airpipe/internal/qr"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

const defaultRelay = "https://airpipe.sanyamgarg.com"

// ANSI escape codes
const (
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorDim   = "\033[2m"
	colorBold  = "\033[1m"
	colorReset = "\033[0m"
)

func banner(mode string) {
	fmt.Fprintf(os.Stderr, "\n  %s%s    _   _     %s___  _          %s\n", colorBold, colorCyan, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s   /_\\ (_)_ _|%s _ \\(_)_ __  ___  %s\n", colorBold, colorCyan, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s  / _ \\| | '_|%s  _/| | '_ \\/ -_) %s\n", colorBold, colorCyan, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s /_/ \\_\\_|_| |%s_|  |_| .__/\\___| %s\n", colorBold, colorCyan, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s             %s      |_|    %s%s%s\n\n", colorBold, colorCyan, colorReset, colorDim, mode, colorReset)
}

func main() {
	relay := flag.String("relay", defaultRelay, "Relay server URL")
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Printf("Usage: %sairpipe%s send <file> [file2...]\n", colorBold, colorReset)
		fmt.Printf("       %sairpipe%s receive [dir]\n", colorBold, colorReset)
		fmt.Printf("       %sairpipe%s download <WORD WORD WORD NN> [dir]\n", colorBold, colorReset)
		fmt.Printf("       %sairpipe%s update\n", colorBold, colorReset)
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "send":
		if len(args) < 2 {
			fmt.Println("Usage: airpipe send <file> [file2...]")
			os.Exit(1)
		}
		err = cmdSend(*relay, args[1:])
	case "receive":
		dir := "."
		if len(args) >= 2 {
			dir = args[1]
		}
		err = cmdReceive(*relay, dir)
	case "download":
		if len(args) < 2 {
			fmt.Println("Usage: airpipe download <WORD WORD WORD NN> [dir]")
			os.Exit(1)
		}
		err = cmdDownload(*relay, args[1:])
	case "update":
		err = cmdUpdate()
	default:
		fmt.Printf("Unknown command: %s\n", args[0])
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n  %s✗ Error: %v%s\n\n", colorRed, err, colorReset)
		os.Exit(1)
	}
}

func cmdSend(relay string, files []string) error {
	// Validate all paths exist
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("not found: %s", f)
		}
	}

	// Determine if we need to zip
	needsZip := len(files) > 1
	if !needsZip {
		info, _ := os.Stat(files[0])
		needsZip = info.IsDir()
	}

	var uploadPath, filename string
	if needsZip {
		banner("send")
		fmt.Printf("  Zipping %d items...", len(files))

		zipPath, err := archive.ZipPaths(files)
		if err != nil {
			return fmt.Errorf("zip failed: %w", err)
		}
		defer os.Remove(zipPath)
		uploadPath = zipPath
		filename = "airpipe-transfer.zip"

		stat, _ := os.Stat(zipPath)
		fmt.Printf("\r  Zipped %d items %s✓%s  %s(%s)%s\n", len(files), colorGreen, colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
	} else {
		uploadPath = files[0]
		filename = filepath.Base(files[0])
		stat, _ := os.Stat(uploadPath)

		banner("send")
		fmt.Printf("  %s%s%s  %s%s%s\n", colorBold, filename, colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
	}

	// Generate passphrase and derive token + key
	phrase := passphrase.Generate()
	derivedToken := passphrase.DeriveToken(phrase)
	derivedKey := passphrase.DeriveKey(phrase)

	// Encrypt the file: [4-byte filename len][filename][content]
	fmt.Print("  Encrypting...")
	plaintext, err := os.ReadFile(uploadPath)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}

	fnBytes := []byte(filename)
	payload := &bytes.Buffer{}
	binary.Write(payload, binary.BigEndian, uint32(len(fnBytes)))
	payload.Write(fnBytes)
	payload.Write(plaintext)

	ciphertext, err := crypto.Encrypt(payload.Bytes(), derivedKey[:])
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	fmt.Printf("\r  Encrypted %s✓%s\n", colorGreen, colorReset)
	fmt.Print("  Uploading...\n\n")

	httpRelay := toHTTP(relay)
	token, err := uploadEncrypted(httpRelay, ciphertext, derivedToken)
	if err != nil {
		return err
	}

	// Display passphrase prominently
	fmt.Printf("  %s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  %s%s║  %-40s║%s\n", colorBold, colorCyan, phrase, colorReset)
	fmt.Printf("  %s%s╚══════════════════════════════════════════╝%s\n\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  Tell them: %s%s%s\n", colorBold, httpRelay, colorReset)
	fmt.Printf("  They type the code, they get the file.\n\n")

	// Also show QR + URL as fallback for nearby devices
	url := fmt.Sprintf("%s/d/%s#%s", httpRelay, token, crypto.KeyToBase64(derivedKey[:]))
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s%s%s%s\n\n", colorDim, "Direct link: ", colorReset, url)
	fmt.Printf("  %sE2E encrypted. Expires in 10 minutes.%s\n\n", colorDim, colorReset)

	return nil
}

func cmdReceive(relay, destDir string) error {
	token := genToken()
	key, _ := crypto.GenerateKey()

	wsRelay := toWS(relay)
	httpRelay := toHTTP(relay)
	url := fmt.Sprintf("%s/u/%s#%s", httpRelay, token, crypto.KeyToBase64(key))

	banner("receive")
	fmt.Printf("  Destination: %s%s%s\n\n", colorBold, destDir, colorReset)
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s%s%s\n\n  %sWaiting for sender...%s\n\n", colorCyan, url, colorReset, colorDim, colorReset)

	receiver := transfer.NewReceiver(wsRelay, token, key)
	if err := receiver.Connect(); err != nil {
		return err
	}
	defer receiver.Close()

	savedPath, err := receiver.ReceiveFile(destDir, progress)
	if err != nil {
		return err
	}
	fmt.Printf("\n  %s✓ Saved: %s%s\n\n", colorGreen, savedPath, colorReset)
	return nil
}

func cmdDownload(relay string, args []string) error {
	// Parse passphrase: last arg might be a directory, or part of the passphrase
	// Passphrase is typically 5 tokens: WORD WORD WORD WORD NN
	// Try to detect if last arg is a directory
	destDir := "."
	phraseArgs := args

	if len(args) > 1 {
		last := args[len(args)-1]
		if info, err := os.Stat(last); err == nil && info.IsDir() {
			destDir = last
			phraseArgs = args[:len(args)-1]
		}
	}

	phrase := strings.Join(phraseArgs, " ")
	derivedToken := passphrase.DeriveToken(phrase)
	derivedKey := passphrase.DeriveKey(phrase)

	banner("download")
	fmt.Printf("  Passphrase: %s%s%s\n", colorCyan, passphrase.Normalize(phrase), colorReset)
	fmt.Printf("  Destination: %s%s%s\n\n", colorBold, destDir, colorReset)
	fmt.Print("  Fetching...")

	httpRelay := toHTTP(relay)
	resp, err := http.Get(httpRelay + "/raw/" + derivedToken)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("file not found or expired. Check the passphrase and try again")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}

	ciphertext, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Printf("\r  Fetched %s✓%s  %s(%s)%s\n", colorGreen, colorReset, colorDim, fmtBytes(int64(len(ciphertext))), colorReset)

	// Decrypt
	fmt.Print("  Decrypting...")
	plaintext, err := crypto.Decrypt(ciphertext, derivedKey[:])
	if err != nil {
		return fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
	}
	fmt.Printf("\r  Decrypted %s✓%s\n", colorGreen, colorReset)

	// Parse payload: [4-byte filename len][filename][content]
	if len(plaintext) < 4 {
		return fmt.Errorf("invalid payload")
	}
	fnLen := int(binary.BigEndian.Uint32(plaintext[:4]))
	if len(plaintext) < 4+fnLen {
		return fmt.Errorf("invalid payload")
	}
	filename := string(plaintext[4 : 4+fnLen])
	content := plaintext[4+fnLen:]

	// Save file, avoid overwriting
	savePath := filepath.Join(destDir, filename)
	if _, err := os.Stat(savePath); err == nil {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		ext := filepath.Ext(filename)
		for i := 1; ; i++ {
			savePath = filepath.Join(destDir, fmt.Sprintf("%s_%d%s", base, i, ext))
			if _, err := os.Stat(savePath); os.IsNotExist(err) {
				break
			}
		}
	}

	if err := os.WriteFile(savePath, content, 0644); err != nil {
		return fmt.Errorf("save failed: %w", err)
	}

	fmt.Printf("\n  %s✓ Saved: %s%s\n\n", colorGreen, savePath, colorReset)
	return nil
}

func cmdUpdate() error {
	banner("update")

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	url := fmt.Sprintf("https://github.com/Sanyam-G/Airpipe/releases/latest/download/airpipe-%s-%s", goos, goarch)

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
	tmpPath := filepath.Join(os.TempDir(), "airpipe-update")
	if err := os.WriteFile(tmpPath, binary, 0755); err != nil {
		return fmt.Errorf("write to temp failed: %w", err)
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

func uploadEncrypted(baseURL string, ciphertext []byte, clientToken string) (string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	total := int64(len(ciphertext))
	errCh := make(chan error, 1)
	go func() {
		// Send the client-derived token
		if clientToken != "" {
			if err := mw.WriteField("token", clientToken); err != nil {
				pw.CloseWithError(err)
				errCh <- err
				return
			}
		}

		part, err := mw.CreateFormFile("file", "encrypted.bin")
		if err != nil {
			pw.CloseWithError(err)
			errCh <- err
			return
		}

		reader := bytes.NewReader(ciphertext)
		buf := make([]byte, 32*1024)
		var written int64
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				if _, writeErr := part.Write(buf[:n]); writeErr != nil {
					pw.CloseWithError(writeErr)
					errCh <- writeErr
					return
				}
				written += int64(n)
				progress(written, total)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				pw.CloseWithError(readErr)
				errCh <- readErr
				return
			}
		}

		mw.Close()
		pw.Close()
		errCh <- nil
	}()

	req, _ := http.NewRequest("POST", baseURL+"/upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if uploadErr := <-errCh; uploadErr != nil {
		return "", uploadErr
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	fmt.Printf("\r  %s✓ Uploaded%s                                      \n\n", colorGreen, colorReset)
	return result.Token, nil
}

func genToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func toHTTP(url string) string {
	url = strings.Replace(url, "wss://", "https://", 1)
	url = strings.Replace(url, "ws://", "http://", 1)
	return url
}

func toWS(url string) string {
	url = strings.Replace(url, "https://", "wss://", 1)
	url = strings.Replace(url, "http://", "ws://", 1)
	return url
}

func fmtBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}

func progress(sent, total int64) {
	pct := float64(sent) / float64(total) * 100
	filled := int(pct / 2.5)
	if filled > 40 {
		filled = 40
	}
	bar := colorCyan + strings.Repeat("█", filled) + colorReset + strings.Repeat("░", 40-filled)
	fmt.Fprintf(os.Stderr, "\r  [%s] %3.0f%% %s%s/%s%s", bar, pct, colorDim, fmtBytes(sent), fmtBytes(total), colorReset)
}
