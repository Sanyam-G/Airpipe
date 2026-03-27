# AirPipe

Transfer files between terminal and any device with a QR code. No apps. No accounts.
```
$ airpipe send config.yaml
```

![demo](demo.gif)

Scan the QR, file downloads. Done.

## Install
```bash
# Auto-detect OS/arch
curl -sL https://raw.githubusercontent.com/Sanyam-G/Airpipe/main/install.sh | sh

# From source
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

**Receive file (phone to server):**
```bash
airpipe receive ./downloads
```

## How it works

Both directions are end-to-end encrypted. The relay only sees ciphertext.

**Send:** CLI encrypts file locally (NaCl secretbox), uploads ciphertext to relay, shows QR code. Encryption key lives in the URL fragment (`#...`) and never reaches the server. Browser decrypts on download.

**Receive:** CLI opens a WebSocket room, shows QR. Phone scans, selects file, encrypts in browser, streams through relay. CLI decrypts locally.

Files expire after 10 minutes. Relay is zero-knowledge.

## Self-host relay
```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
airpipe --relay https://your-server:8080 send file.txt
```

## License

MIT
