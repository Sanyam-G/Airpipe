package main

import (
	"bufio"
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
	"time"

	"github.com/sanyamgarg/airpipe/internal/archive"
	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/mailbox"
	"github.com/sanyamgarg/airpipe/internal/passphrase"
	"github.com/sanyamgarg/airpipe/internal/qr"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

const defaultRelay = "https://airpipe.sanyamgarg.com"

// ANSI escape codes
const (
	colorBrand = "\033[38;2;255;79;0m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorDim   = "\033[2m"
	colorBold  = "\033[1m"
	colorReset = "\033[0m"
)

func printUsage() {
	fmt.Printf("Usage: %sairpipe%s send [--stay-open] [--mode p2p|mailbox] <file> [file2...]\n", colorBold, colorReset)
	fmt.Printf("       %sairpipe%s receive [--stay-open] [dir]\n", colorBold, colorReset)
	fmt.Printf("       %sairpipe%s download [--stay-open] <WORD WORD WORD NN> [dir]\n", colorBold, colorReset)
	fmt.Printf("       %sairpipe%s update\n", colorBold, colorReset)
	fmt.Printf("       %sairpipe%s help\n\n", colorBold, colorReset)
	fmt.Printf("Run %sairpipe help%s for details.\n", colorBold, colorReset)
}

func printHelp() {
	b := colorBold
	r := colorReset
	d := colorDim
	fmt.Printf("\n%sairpipe%s — peer-to-peer encrypted file transfer\n\n", b, r)

	fmt.Printf("%sCommands%s\n", b, r)
	fmt.Printf("  %ssend%s [--stay-open] [--mode p2p|mailbox] <file> [file2...]\n", b, r)
	fmt.Printf("      Encrypt and share a file. You get a passphrase like %sRIVER FALCON MARBLE 42%s.\n", b, r)
	fmt.Printf("      In %sp2p%s mode, multiple files stream in one session; a %sfolder%s is zipped.\n", b, r, b, r)
	fmt.Printf("      In %smailbox%s mode, multiple files ship in one upload without zipping;\n", b, r)
	fmt.Printf("        %sfolders%s (or mixes with folders) still become one zip upload.\n", b, r)
	fmt.Printf("        %s--mode p2p%s      Stream directly between sender and receiver over WebRTC.\n", b, r)
	fmt.Printf("        %s--mode mailbox%s  Upload to the relay; receiver downloads later.\n", b, r)
	fmt.Printf("                        10-minute expiry, 500 MB cap.\n")
	fmt.Printf("        %s--stay-open%s    After a p2p batch, prompt for more files (receiver needs %s--stay-open%s too).\n", b, r, b, r)
	fmt.Printf("        %s(default: prompt)%s\n\n", d, r)

	fmt.Printf("  %sreceive%s [--stay-open] [dir]\n", b, r)
	fmt.Printf("      Wait for someone to send a file to you. Defaults to current directory.\n")
	fmt.Printf("      %s--stay-open%s keeps the session open for more batches.\n\n", b, r)

	fmt.Printf("  %sdownload%s [--stay-open] <WORD WORD WORD NN> [dir]\n", b, r)
	fmt.Printf("      Download mailbox or live payloads using a passphrase someone shared.\n")
	fmt.Printf("      Mailbox bundles with several files decrypt to separate saves.\n")
	fmt.Printf("      With live p2p, %s--stay-open%s waits for extra batches after the first.\n\n", b, r)

	fmt.Printf("  %supdate%s\n", b, r)
	fmt.Printf("      Self-update the CLI binary in place.\n\n")

	fmt.Printf("  %shelp%s\n", b, r)
	fmt.Printf("      Show this message.\n\n")

	fmt.Printf("%sFlags%s\n", b, r)
	fmt.Printf("  %s--relay%s <origin>\n", b, r)
	fmt.Printf("      Use a relay other than the default for this call.\n")
	fmt.Printf("      Permanent: %sexport AIRPIPE_RELAY=https://your-relay.example%s\n\n", b, r)

	fmt.Printf("%sExamples%s\n", b, r)
	fmt.Printf("  airpipe send report.pdf\n")
	fmt.Printf("  airpipe send report.pdf notes.txt    %s# p2p: two files, one connection%s\n", d, r)
	fmt.Printf("  airpipe send photos/ docs/          %s# mailed or p2p with dirs: zip%s\n", d, r)
	fmt.Printf("  airpipe download RIVER FALCON MARBLE 42\n")
	fmt.Printf("  airpipe receive ~/Downloads\n")
	fmt.Printf("  airpipe --relay https://my.relay send a.zip\n\n")

	fmt.Printf("%sLinks%s\n", b, r)
	fmt.Printf("  Source        github.com/Sanyam-G/Airpipe\n")
	fmt.Printf("  Web sender    https://airpipe.sanyamgarg.com\n\n")
}

func banner(mode string) {
	fmt.Fprintf(os.Stderr, "\n  %s%s    _   _     %s___  _          %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s   /_\\ (_)_ _|%s _ \\(_)_ __  ___  %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s  / _ \\| | '_|%s  _/| | '_ \\/ -_) %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s /_/ \\_\\_|_| |%s_|  |_| .__/\\___| %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s             %s      |_|    %s%s%s\n\n", colorBold, colorBrand, colorReset, colorDim, mode, colorReset)
}

func main() {
	defaultRelayURL := defaultRelay
	if env := strings.TrimSpace(os.Getenv("AIRPIPE_RELAY")); env != "" {
		defaultRelayURL = env
	}
	relay := flag.String("relay", defaultRelayURL, "Relay server URL (or set AIRPIPE_RELAY)")
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "send":
		if len(args) < 2 {
			fmt.Println("Usage: airpipe send [--stay-open] [--mode p2p|mailbox] <file> [file2...]")
			os.Exit(1)
		}
		err = cmdSend(*relay, args[1:])
	case "receive":
		stayOpen, rest := parseStayOpenFlags(args[1:])
		dir := "."
		if len(rest) >= 1 {
			dir = rest[0]
		}
		err = cmdReceive(*relay, dir, stayOpen)
	case "download":
		if len(args) < 2 {
			fmt.Println("Usage: airpipe download [--stay-open] <WORD WORD WORD NN> [dir]")
			os.Exit(1)
		}
		err = cmdDownload(*relay, args[1:])
	case "update":
		err = cmdUpdate()
	case "help", "--help", "-h":
		printHelp()
		return
	default:
		fmt.Printf("Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n  %s✗ Error: %v%s\n\n", colorRed, err, colorReset)
		os.Exit(1)
	}
}

func parseStayOpenFlags(args []string) (stayOpen bool, rest []string) {
	for _, a := range args {
		if a == "--stay-open" {
			stayOpen = true
			continue
		}
		rest = append(rest, a)
	}
	return stayOpen, rest
}

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

	fnBytes := []byte(filename)
	payload := &bytes.Buffer{}
	binary.Write(payload, binary.BigEndian, uint32(len(fnBytes)))
	payload.Write(fnBytes)
	payload.Write(plaintext)

	ciphertext, err := crypto.Encrypt(payload.Bytes(), derivedKey)
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

func displayPassphrase(phrase, httpRelay, token string, key []byte) {
	fmt.Printf("  %s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorBrand, colorReset)
	fmt.Printf("  %s%s║  %-40s║%s\n", colorBold, colorBrand, phrase, colorReset)
	fmt.Printf("  %s%s╚══════════════════════════════════════════╝%s\n\n", colorBold, colorBrand, colorReset)
	fmt.Printf("  Tell them: %s%s%s\n", colorBold, httpRelay, colorReset)
	fmt.Printf("  Or run:    %sairpipe download %s%s\n\n", colorBold, phrase, colorReset)

	url := fmt.Sprintf("%s/d/%s#%s", httpRelay, token, crypto.KeyToBase64(key))
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %sDirect link:%s %s\n\n", colorDim, colorReset, url)
}

func cmdReceive(relay, destDir string, stayOpen bool) error {
	phrase := passphrase.Generate()
	token := passphrase.DeriveReceiveToken(phrase)
	keyArr := passphrase.DeriveKey(phrase)
	key := keyArr[:]

	wsRelay := toWS(relay)
	httpRelay := toHTTP(relay)
	url := fmt.Sprintf("%s/u/%s#%s", httpRelay, token, crypto.KeyToBase64(key))

	banner("receive")
	fmt.Printf("  Destination: %s%s%s\n\n", colorBold, destDir, colorReset)
	if stayOpen {
		fmt.Printf("  %sStay-open:%s waiting for multiple batches on this link (Ctrl+C to stop).\n\n", colorDim, colorReset)
	}
	fmt.Printf("  %s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorBrand, colorReset)
	fmt.Printf("  %s%s║  %-40s║%s\n", colorBold, colorBrand, phrase, colorReset)
	fmt.Printf("  %s%s╚══════════════════════════════════════════╝%s\n\n", colorBold, colorBrand, colorReset)
	fmt.Printf("  Tell the sender: type this at %s%s%s\n\n", colorBold, httpRelay, colorReset)
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s%s%s\n\n  %sWaiting for sender...%s\n\n", colorBrand, url, colorReset, colorDim, colorReset)

	receiver := transfer.NewReceiver(wsRelay, token, key)
	if err := receiver.Connect(); err != nil {
		return err
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
			return err
		}
		fmt.Println()
		return nil
	}

	savedPaths, err := receiver.ReceiveFiles(destDir, func(_ int, received, total int64) {
		progress(received, total)
	})
	if err != nil {
		return err
	}
	for _, savedPath := range savedPaths {
		fmt.Printf("\n  %s✓ Saved: %s%s\n", colorGreen, savedPath, colorReset)
	}
	fmt.Println()
	return nil
}

func cmdDownload(relay string, args []string) error {
	stayOpen, args := parseStayOpenFlags(args)
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

func cmdUpdate() error {
	banner("update")

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	url := fmt.Sprintf("https://github.com/Sanyam-G/Airpipe/releases/latest/download/airpipe-%s-%s%s", goos, goarch, ext)

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
	bar := colorBrand + strings.Repeat("█", filled) + colorReset + strings.Repeat("░", 40-filled)
	fmt.Fprintf(os.Stderr, "\r  [%s] %3.0f%% %s%s/%s%s", bar, pct, colorDim, fmtBytes(sent), fmtBytes(total), colorReset)
}
