package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const defaultRelay = "https://airpipe.sanyamgarg.com"

// buildVersion is set via -ldflags at build time.
var buildVersion = "dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("airpipe %s (%s/%s)\n", buildVersion, runtime.GOOS, runtime.GOARCH)
			return
		}
	}

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

func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
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
