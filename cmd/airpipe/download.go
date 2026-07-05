package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/mailbox"
	"github.com/sanyamgarg/airpipe/internal/passphrase"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

func cmdDownload(relay string, args []string) error {
	stayOpen, args := parseStayOpenFlags(args)
	// The last arg may be a destination dir rather than part of the passphrase.
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
	fmt.Printf("  Passphrase: %s%s%s\n", colorBrand, passphrase.Normalize(phrase), colorReset)
	fmt.Printf("  Destination: %s%s%s\n", colorBold, destDir, colorReset)
	if stayOpen {
		fmt.Printf("  %sStay-open:%s will wait for more batches if the sender uses a live session.\n", colorDim, colorReset)
	}
	fmt.Println()
	fmt.Print("  Looking up...")

	httpRelay := toHTTP(relay)
	resp, err := http.Head(httpRelay + "/raw/" + derivedToken)
	if err != nil {
		return fmt.Errorf("relay unreachable: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// nothing in the mailbox, try the P2P room
		fmt.Printf("\r  %sNo mailbox transfer; opening direct connection...%s\n\n", colorDim, colorReset)
		wsRelay := toWS(relay)
		receiver := transfer.NewReceiver(wsRelay, derivedToken, derivedKey[:])
		if err := receiver.ConnectLive(); err != nil {
			return fmt.Errorf("no transfer found for that passphrase. Either the link expired, the sender gave up, or the passphrase is wrong")
		}
		defer receiver.Close()

		if stayOpen {
			err := receiver.ReceiveBatches(destDir, func(batchIdx int, paths []string) error {
				if batchIdx > 0 {
					fmt.Printf("\n  %s--- batch %d ---%s\n", colorDim, batchIdx+1, colorReset)
				}
				for _, savedPath := range paths {
					fmt.Printf("\n  %s✓ Saved: %s%s\n", colorGreen, savedPath, colorReset)
				}
				return nil
			}, func(_ int, received, total int64) {
				progress(received, total)
			})
			if err != nil {
				return fmt.Errorf("p2p receive: %w", err)
			}
			fmt.Println()
			return nil
		}

		savedPaths, err := receiver.ReceiveFiles(destDir, func(_ int, received, total int64) {
			progress(received, total)
		})
		if err != nil {
			return fmt.Errorf("p2p receive: %w", err)
		}
		for _, savedPath := range savedPaths {
			fmt.Printf("\n  %s✓ Saved: %s%s\n", colorGreen, savedPath, colorReset)
		}
		fmt.Println()
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}

	// pull the ciphertext blob
	fmt.Print("\r  Fetching...   ")
	getResp, err := http.Get(httpRelay + "/raw/" + derivedToken)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error: %d", getResp.StatusCode)
	}

	ciphertext, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	fmt.Printf("\r  Fetched %s✓%s  %s(%s)%s\n", colorGreen, colorReset, colorDim, fmtBytes(int64(len(ciphertext))), colorReset)

	fmt.Print("  Decrypting...")
	plaintext, err := crypto.Decrypt(ciphertext, derivedKey[:])
	if err != nil {
		return fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
	}
	fmt.Printf("\r  Decrypted %s✓%s\n", colorGreen, colorReset)

	entries, err := mailbox.Decode(plaintext)
	if err != nil {
		return fmt.Errorf("invalid mailbox payload: %w", err)
	}
	for _, ent := range entries {
		safe, err := transfer.SafeFilename(ent.Name)
		if err != nil {
			return fmt.Errorf("unsafe filename %q: %w", ent.Name, err)
		}
		savePath := mailboxUniqueSavePath(destDir, safe)
		if err := os.WriteFile(savePath, ent.Content, 0644); err != nil {
			return fmt.Errorf("save failed: %w", err)
		}
		fmt.Printf("\n  %s✓ Saved: %s%s\n", colorGreen, savePath, colorReset)
	}
	fmt.Println()
	return nil
}

func mailboxUniqueSavePath(destDir, safeFilename string) string {
	p := filepath.Join(destDir, safeFilename)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(safeFilename)
	base := strings.TrimSuffix(safeFilename, ext)
	for i := 1; ; i++ {
		p = filepath.Join(destDir, fmt.Sprintf("%s(%d)%s", base, i, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
}
