# AirPipe

Encrypted file transfer. No accounts. No apps.

```
$ airpipe send config.yaml
```

![demo](demo.gif)

Scan the QR, file downloads. Done.

**Try it now:** [airpipe.sanyamgarg.com/send](https://airpipe.sanyamgarg.com/send) - send files from your browser, no install needed.

## Install

```bash
curl -sL https://raw.githubusercontent.com/Sanyam-G/Airpipe/main/install.sh | sh
```

Or from source:
```bash
go install github.com/Sanyam-G/Airpipe/cmd/airpipe@latest
```

## Usage

**Send a file:**
```bash
airpipe send ./error.log
```

**Send multiple files (auto-zipped):**
```bash
airpipe send file1.txt file2.txt photos/
```

**Receive a file (phone to server):**
```bash
airpipe receive ./downloads
```

**Send from browser (no CLI needed):**

Go to [airpipe.sanyamgarg.com/send](https://airpipe.sanyamgarg.com/send), drop a file, share the link.

## How it works

Everything is end-to-end encrypted. The relay is zero-knowledge.

**Send:** CLI encrypts locally (NaCl secretbox), uploads ciphertext to relay, prints a QR code. The encryption key lives in the URL fragment (`#...`) and never reaches the server. Browser decrypts on download.

**Receive:** CLI opens a WebSocket room and prints a QR. Phone scans, selects a file, encrypts in browser, streams chunks through relay. CLI decrypts locally.

**Web send:** Browser generates a key, encrypts the file client-side, uploads ciphertext. Same zero-knowledge guarantee as the CLI.

Files expire after 10 minutes.

## Self-host

```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
airpipe --relay https://your-server:8080 send file.txt
```

The relay serves the landing page, web send, and download pages. One container, everything included.

## License

MIT
