package main

import (
	"fmt"

	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/passphrase"
	"github.com/sanyamgarg/airpipe/internal/qr"
	"github.com/sanyamgarg/airpipe/internal/transfer"
)

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
