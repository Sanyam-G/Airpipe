# AirPipe

Transfer files between terminal and any device with a QR code. No apps. No accounts.
```
$ airpipe send config.yaml
```

![demo](demo.gif)

Scan or wget the link. Done.

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

**Download on the other end:**
```bash
# Scan QR, or use wget/curl
wget https://airpipe.sanyamgarg.com/d/a1b2c3
curl -O https://airpipe.sanyamgarg.com/d/a1b2c3
```

**Receive file (phone to server):**
```bash
airpipe receive ./downloads
```

## How it works

**Send:** CLI uploads file to relay, prints a short URL + QR code. Anyone with the link can download via browser, wget, or curl. Files expire after 10 minutes.

**Receive:** CLI opens a WebSocket room, shows QR. Phone scans, selects file, uploads through browser. File is E2E encrypted (NaCl secretbox, key in URL fragment).

## Self-host relay
```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
airpipe --relay https://your-server:8080 send file.txt
```

## License

MIT
