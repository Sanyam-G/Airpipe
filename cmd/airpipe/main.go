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

func main() {
	relay := flag.String("relay", defaultRelay, "Relay server URL")
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Println("Usage: airpipe send <file> [file2...] | airpipe receive [dir]")
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
		fmt.Printf("\n  ✗ Error: %v\n\n", err)
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
		fmt.Print("\n  AirPipe - Send\n\n")
		fmt.Printf("  Zipping %d items...\n", len(files))

		zipPath, err := archive.ZipPaths(files)
		if err != nil {
			return fmt.Errorf("zip failed: %w", err)
		}
		defer os.Remove(zipPath)
		uploadPath = zipPath
		filename = "airpipe-transfer.zip"

		stat, _ := os.Stat(zipPath)
		fmt.Printf("  Archive: %s\n\n", fmtBytes(stat.Size()))
	} else {
		uploadPath = files[0]
		filename = filepath.Base(files[0])
		stat, _ := os.Stat(uploadPath)

		fmt.Print("\n  AirPipe - Send\n\n")
		fmt.Printf("  File: %s (%s)\n\n", filename, fmtBytes(stat.Size()))
	}

	// Encrypt the file: [4-byte filename len][filename][content]
	fmt.Print("  Encrypting...\n")
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

	fmt.Print("  Uploading...\n\n")

	httpRelay := toHTTP(relay)
	token, err := uploadEncrypted(httpRelay, ciphertext)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/d/%s#%s", httpRelay, token, crypto.KeyToBase64(key))
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s\n\n", url)
	fmt.Print("  E2E encrypted. Expires in 10 minutes.\n\n")

	return nil
}

func cmdReceive(relay, destDir string) error {
	token := genToken()
	key, _ := crypto.GenerateKey()

	wsRelay := toWS(relay)
	httpRelay := toHTTP(relay)
	url := fmt.Sprintf("%s/u/%s#%s", httpRelay, token, crypto.KeyToBase64(key))

	fmt.Print("\n  AirPipe - Receive\n\n")
	fmt.Printf("  Destination: %s\n\n", destDir)
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s\n\n  Waiting...\n\n", url)

	receiver := transfer.NewReceiver(wsRelay, token, key)
	if err := receiver.Connect(); err != nil {
		return err
	}
	defer receiver.Close()

	savedPath, err := receiver.ReceiveFile(destDir, progress)
	if err != nil {
		return err
	}
	fmt.Printf("\n  ✓ Saved: %s\n\n", savedPath)
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

	fmt.Print("\r  ✓ Uploaded                                      \n\n")
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
	fmt.Printf("\r  [%-40s] %.0f%%", strings.Repeat("█", int(pct/2.5))+strings.Repeat("░", 40-int(pct/2.5)), pct)
}
