package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

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
