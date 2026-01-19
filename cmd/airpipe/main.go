package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/qr"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

const defaultRelay = "wss://airpipe.sanyamgarg.com"

func main() {
	relay := flag.String("relay", defaultRelay, "Relay server URL")
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Println("Usage: airpipe send <file> | airpipe receive [dir]")
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "send":
		if len(args) < 2 {
			fmt.Println("Usage: airpipe send <file>")
			os.Exit(1)
		}
		err = cmdSend(*relay, args[1])
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

func cmdSend(relay, filePath string) error {
	stat, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %s", filePath)
	}

	token := genToken()
	key, _ := crypto.GenerateKey()

	httpRelay := strings.Replace(strings.Replace(relay, "wss://", "https://", 1), "ws://", "http://", 1)
	url := fmt.Sprintf("%s/d/%s#%s", httpRelay, token, crypto.KeyToBase64(key))

	fmt.Println("\n  AirPipe - Send\n")
	fmt.Printf("  File: %s (%s)\n\n", stat.Name(), fmtBytes(stat.Size()))
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s\n\n  🔒 E2E Encrypted\n  ⏳ Waiting...\n\n", url)

	sender := transfer.NewSender(relay, token, key)
	if err := sender.Connect(); err != nil {
		return err
	}
	defer sender.Close()

	if err := sender.WaitForReceiver(5 * time.Minute); err != nil {
		return err
	}
	fmt.Println("  ✓ Connected!\n")

	if err := sender.SendFile(filePath, progress); err != nil {
		return err
	}
	fmt.Println("\n  ✓ Done!\n")
	return nil
}

func cmdReceive(relay, destDir string) error {
	token := genToken()
	key, _ := crypto.GenerateKey()

	httpRelay := strings.Replace(strings.Replace(relay, "wss://", "https://", 1), "ws://", "http://", 1)
	url := fmt.Sprintf("%s/u/%s#%s", httpRelay, token, crypto.KeyToBase64(key))

	fmt.Println("\n  AirPipe - Receive\n")
	fmt.Printf("  Destination: %s\n\n", destDir)
	qr.GenerateTerminal(url)
	fmt.Printf("\n  %s\n\n  🔒 E2E Encrypted\n  ⏳ Waiting...\n\n", url)

	receiver := transfer.NewReceiver(relay, token, key)
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

func genToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
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
