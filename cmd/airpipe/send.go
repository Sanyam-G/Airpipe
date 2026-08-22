package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sanyamgarg/airpipe/internal/archive"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/mailbox"
	"github.com/sanyamgarg/airpipe/internal/passphrase"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

func cmdSend(relay string, args []string) error {
	sendFS := flag.NewFlagSet("send", flag.ContinueOnError)
	mode := sendFS.String("mode", "", "p2p | mailbox (default: prompt)")
	stayOpen := sendFS.Bool("stay-open", false, "after each p2p batch, prompt for more files (requires receiver --stay-open)")
	if err := sendFS.Parse(args); err != nil {
		return err
	}
	files := sendFS.Args()
	if len(files) == 0 {
		return fmt.Errorf("usage: airpipe send [--stay-open] [--mode p2p|mailbox] <file> [file2...]")
	}

	anyDir := false
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return fmt.Errorf("not found: %s", f)
		}
		if info.IsDir() {
			anyDir = true
		}
	}
	resolvedMode, err := resolveMode(*mode)
	if err != nil {
		return err
	}

	mailboxMultiNative := resolvedMode == "mailbox" && !anyDir && len(files) > 1
	needsZip := anyDir || (len(files) > 1 && !mailboxMultiNative)

	var uploadPath, filename string
	var p2pNative []string
	var mailboxNative []string

	if resolvedMode == "p2p" && !anyDir {
		p2pNative = files
		banner("send")
		for _, f := range files {
			stat, _ := os.Stat(f)
			fmt.Printf("  %s%s%s  %s%s%s\n", colorBold, filepath.Base(f), colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
		}
		if len(files) > 1 {
			fmt.Println()
		}
	} else if mailboxMultiNative {
		mailboxNative = files
		banner("send")
		for _, f := range files {
			stat, _ := os.Stat(f)
			fmt.Printf("  %s%s%s  %s%s%s\n", colorBold, filepath.Base(f), colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
		}
		fmt.Printf("\n  %smailbox%s packing %d files…\n\n", colorDim, colorReset, len(files))
	} else if needsZip {
		banner("send")
		fmt.Printf("  Zipping %d items...", len(files))
		zipPath, err := archive.ZipPaths(files)
		if err != nil {
			return fmt.Errorf("zip failed: %w", err)
		}
		defer os.Remove(zipPath)
		uploadPath = zipPath
		filename = archiveName(files)
		stat, _ := os.Stat(zipPath)
		fmt.Printf("\r  Zipped %d items %s✓%s  %s(%s → %s)%s\n", len(files), colorGreen, colorReset, colorDim, filename, fmtBytes(stat.Size()), colorReset)
	} else {
		uploadPath = files[0]
		filename = filepath.Base(files[0])
		stat, _ := os.Stat(uploadPath)
		banner("send")
		fmt.Printf("  %s%s%s  %s%s%s\n", colorBold, filename, colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
	}

	phrase := passphrase.Generate()
	derivedToken := passphrase.DeriveToken(phrase)
	derivedKeyArr := passphrase.DeriveKey(phrase)
	derivedKey := derivedKeyArr[:]

	if *stayOpen && resolvedMode != "p2p" {
		return fmt.Errorf("--stay-open is only supported in p2p mode (--mode p2p)")
	}

	httpRelay := toHTTP(relay)
	wsRelay := toWS(relay)

	switch resolvedMode {
	case "p2p":
		if p2pNative != nil {
			return sendP2P(wsRelay, httpRelay, p2pNative, phrase, derivedToken, derivedKey, *stayOpen)
		}
		return sendP2P(wsRelay, httpRelay, []string{uploadPath}, phrase, derivedToken, derivedKey, *stayOpen)
	case "mailbox":
		if len(mailboxNative) > 0 {
			return sendMailboxNative(httpRelay, mailboxNative, phrase, derivedToken, derivedKey)
		}
		return sendMailbox(httpRelay, uploadPath, filename, phrase, derivedToken, derivedKey)
	}
	return fmt.Errorf("invalid mode: %s", resolvedMode)
}

// archiveName: a single folder keeps its name, anything else gets an item count.
func archiveName(paths []string) string {
	if len(paths) == 1 {
		base := filepath.Base(filepath.Clean(paths[0]))
		if base != "." && base != string(filepath.Separator) && base != "" {
			return base + ".zip"
		}
	}
	return fmt.Sprintf("airpipe-%d-items.zip", len(paths))
}

func resolveMode(flagVal string) (string, error) {
	if flagVal == "p2p" || flagVal == "mailbox" {
		return flagVal, nil
	}
	if flagVal != "" {
		return "", fmt.Errorf("invalid --mode %q (use p2p or mailbox)", flagVal)
	}
	stat, _ := os.Stdin.Stat()
	if stat.Mode()&os.ModeCharDevice == 0 {
		return "mailbox", nil
	}
	fmt.Printf("\n  Mode?\n")
	fmt.Printf("    %s[1]%s Direct    Both online now, file goes peer-to-peer\n", colorBrand, colorReset)
	fmt.Printf("    %s[2]%s Mailbox   Relay holds it ~10 min, receiver picks up later\n", colorBrand, colorReset)
	fmt.Printf("  Choose %s[1]%s: ", colorBrand, colorReset)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
	switch strings.TrimSpace(line) {
	case "", "1":
		return "p2p", nil
	case "2":
		return "mailbox", nil
	}
	return "", fmt.Errorf("invalid choice: %s", line)
}

func sendMailbox(httpRelay, uploadPath, filename, phrase, derivedToken string, derivedKey []byte) error {
	fmt.Print("  Encrypting...")
	plaintext, err := os.ReadFile(uploadPath)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}

	payload, err := mailbox.EncodeV1(filename, plaintext)
	if err != nil {
		return err
	}

	ciphertext, err := crypto.Encrypt(payload, derivedKey)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	fmt.Printf("\r  Encrypted %s✓%s\n", colorGreen, colorReset)
	fmt.Print("  Uploading...\n\n")

	token, err := uploadEncrypted(httpRelay, ciphertext, derivedToken)
	if err != nil {
		return err
	}

	displayPassphrase(phrase, httpRelay, token, derivedKey)
	fmt.Printf("  %sE2E encrypted. Expires in 10 minutes.%s\n\n", colorDim, colorReset)
	return nil
}

func sendMailboxNative(httpRelay string, paths []string, phrase, derivedToken string, derivedKey []byte) error {
	if len(paths) == 0 {
		return fmt.Errorf("nothing to send")
	}
	fmt.Print("  Encrypting...")
	entries := make([]mailbox.Entry, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		entries = append(entries, mailbox.Entry{Name: filepath.Base(p), Content: raw})
	}
	plaintext, err := mailbox.EncodeAMB2(entries)
	if err != nil {
		return fmt.Errorf("pack files: %w", err)
	}
	ciphertext, err := crypto.Encrypt(plaintext, derivedKey)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	fmt.Printf("\r  Encrypted %s✓%s\n", colorGreen, colorReset)
	fmt.Print("  Uploading...\n\n")

	token, err := uploadEncrypted(httpRelay, ciphertext, derivedToken)
	if err != nil {
		return err
	}

	displayPassphrase(phrase, httpRelay, token, derivedKey)
	fmt.Printf("  %sE2E encrypted. Expires in 10 minutes.%s\n\n", colorDim, colorReset)
	return nil
}

func sendP2P(wsRelay, httpRelay string, paths []string, phrase, derivedToken string, derivedKey []byte, stayOpen bool) error {
	if len(paths) == 0 {
		return fmt.Errorf("nothing to send")
	}
	displayPassphrase(phrase, httpRelay, derivedToken, derivedKey)
	fmt.Printf("  %sWaiting for receiver to join...%s\n\n", colorDim, colorReset)

	sender := transfer.NewSender(wsRelay, derivedToken, derivedKey)
	if err := sender.ConnectLive(); err != nil {
		return fmt.Errorf("connect to relay: %w", err)
	}
	defer sender.Close()

	if err := sender.WaitForPeer(30 * time.Minute); err != nil {
		return fmt.Errorf("waiting for receiver: %w", err)
	}
	fmt.Printf("  %s✓ Receiver joined%s\n", colorGreen, colorReset)

	progressBatch := func(batch []string) func(fileIdx int, sent, total int64) {
		return func(fileIdx int, sent, total int64) {
			if len(batch) > 1 {
				fmt.Fprintf(os.Stderr, "\n  [%d/%d] %s ", fileIdx+1, len(batch), filepath.Base(batch[fileIdx]))
			}
			progress(sent, total)
		}
	}

	sendAndSummarize := func(batch []string) error {
		var sendFn func([]string) error
		if stayOpen {
			sendFn = func(b []string) error {
				return sender.SendBatch(b, progressBatch(b))
			}
		} else {
			sendFn = func(b []string) error {
				return sender.SendFiles(b, progressBatch(b))
			}
		}
		if err := sendFn(batch); err != nil {
			return fmt.Errorf("send file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\r%s\n", strings.Repeat(" ", 80))
		printSentBatchSummary(batch)
		return nil
	}

	batch := paths
	for {
		if err := sendAndSummarize(batch); err != nil {
			return err
		}
		if !stayOpen {
			break
		}
		next, err := readNextSendPaths()
		if err != nil {
			return err
		}
		if len(next) == 0 {
			break
		}
		batch = next
	}
	fmt.Println()
	return nil
}

func printSentBatchSummary(paths []string) {
	if len(paths) == 1 {
		stat, _ := os.Stat(paths[0])
		fmt.Printf("\r  %s✓ Sent %s%s  %s(%s)%s\n", colorGreen, colorReset, filepath.Base(paths[0]), colorDim, fmtBytes(stat.Size()), colorReset)
		return
	}
	fmt.Printf("  %s✓ Sent %d files%s\n", colorGreen, len(paths), colorReset)
	for _, p := range paths {
		stat, _ := os.Stat(p)
		fmt.Printf("    %s%s%s  %s%s%s\n", colorBold, filepath.Base(p), colorReset, colorDim, fmtBytes(stat.Size()), colorReset)
	}
}

func readNextSendPaths() ([]string, error) {
	fmt.Fprintf(os.Stderr, "\n  %sMore files? Enter paths (space-separated), or press Enter to finish:%s ", colorDim, colorReset)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return nil, nil
	}
	var out []string
	for _, p := range fields {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("not found: %s", p)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("folders must be zipped for p2p; choose files only, or start a new send: %s", p)
		}
		out = append(out, p)
	}
	return out, nil
}
