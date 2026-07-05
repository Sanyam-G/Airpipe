package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/sanyamgarg/airpipe/internal/crypto"
	"github.com/sanyamgarg/airpipe/internal/qr"
)

// ANSI escape codes
const (
	colorBrand = "\033[38;2;255;79;0m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorDim   = "\033[2m"
	colorBold  = "\033[1m"
	colorReset = "\033[0m"
)

func banner(mode string) {
	fmt.Fprintf(os.Stderr, "\n  %s%s    _   _     %s___  _          %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s   /_\\ (_)_ _|%s _ \\(_)_ __  ___  %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s  / _ \\| | '_|%s  _/| | '_ \\/ -_) %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s /_/ \\_\\_|_| |%s_|  |_| .__/\\___| %s\n", colorBold, colorBrand, colorReset, colorReset)
	fmt.Fprintf(os.Stderr, "  %s%s             %s      |_|    %s%s%s\n\n", colorBold, colorBrand, colorReset, colorDim, mode, colorReset)
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

func fmtBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(b)/(1024*1024*1024))
	}
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
