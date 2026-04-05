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
	"path/filepath"
	"strings"

	"github.com/sanyamgarg/airpipe/internal/archive"
	"github.com/sanyamgarg/airpipe/internal/crypto"
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
		fmt.Printf("Usage: %sairpipe%s send <file> [file2...] | %sairpipe%s receive [dir]\n",
			colorBold, colorReset, colorBold, colorReset)
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

	// Encrypt the file: [4-byte filename len][filename][content]
	fmt.Print("  Encrypting...")
	plaintext, err := os.ReadFile(uploadPath)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}

	key, _ := crypto.GenerateKey()
	fnBytes := []byte(filename)
	payload := &bytes.Buffer{}
	binary.Write(payload, binary.BigEndian, uint32(len(fnBytes)))
	payload.Write(fnBytes)
	payload.Write(plaintext)

	ciphertext, err := crypto.Encrypt(payload.Bytes(), key)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	fmt.Printf("\r  Encrypted %s✓%s\n", colorGreen, colorReset)
	fmt.Print("  Uploading...\n\n")

	httpRelay := toHTTP(relay)
	token, err := uploadEncrypted(httpRelay, ciphertext)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/d/%s#%s", httpRelay, token, crypto.KeyToBase64(key))
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s%s%s\n\n", colorCyan, url, colorReset)
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

func uploadEncrypted(baseURL string, ciphertext []byte) (string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	total := int64(len(ciphertext))
	errCh := make(chan error, 1)
	go func() {
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
