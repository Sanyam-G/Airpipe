# AirPipe

Encrypted file transfer. No accounts. No apps.

```
$ airpipe send config.yaml

  ╔══════════════════════════════════════════╗
  ║  RIVER FALCON MARBLE 42                 ║
  ╚══════════════════════════════════════════╝

  Tell them: airpipe.sanyamgarg.com
  They type the code, they get the file.
```

**Try it now:** [airpipe.sanyamgarg.com/send](https://airpipe.sanyamgarg.com/send)

## Install

```bash
curl -sSL https://airpipe.sanyamgarg.com/install.sh | sh
```

Or:
```bash
go install github.com/Sanyam-G/Airpipe/cmd/airpipe@latest
```

Update an existing installation:
```bash
airpipe update
```

## Usage

**Send a file:**
```bash
airpipe send photo.jpg
```
Shows a passphrase and a QR code. The receiver either:
- Types the passphrase at [airpipe.sanyamgarg.com](https://airpipe.sanyamgarg.com)
- Scans the QR code
- Runs `airpipe download` from their terminal

**Download with a passphrase:**
```bash
airpipe download RIVER FALCON MARBLE 42
```

**Send multiple files (auto-zipped):**
```bash
airpipe send file1.txt file2.txt photos/
```

**Receive a file (phone to server):**
```bash
airpipe receive ./downloads
```

**Send from browser:**

Go to [airpipe.sanyamgarg.com/send](https://airpipe.sanyamgarg.com/send), drop a file, share the passphrase.

## How it works

Everything is end-to-end encrypted. The relay is zero-knowledge.

1. CLI generates a passphrase (e.g. `RIVER FALCON MARBLE 42`)
2. Token and encryption key are both derived from the passphrase using SHA-256 with domain separation
3. File is encrypted locally with NaCl secretbox, uploaded to the relay as ciphertext
4. Receiver enters the passphrase. Browser (or CLI) derives the same token and key, fetches ciphertext, decrypts locally

The relay only sees a hex token and encrypted bytes. It never sees the passphrase or the key.

Files expire after 10 minutes. QR code and direct URL still work as fallback for nearby devices.

## Self-host

```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
airpipe --relay https://your-server:8080 send file.txt
```

One container. Includes the landing page, web send, download pages, and install script.

## License

MIT
