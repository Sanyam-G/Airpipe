# AirPipe

Self-hosted file transfer with a passphrase that works anywhere. Files go peer-to-peer between any two devices. The relay never sees your bytes.

![demo](demo.gif)

**Try it:** [airpipe.sanyamgarg.com](https://airpipe.sanyamgarg.com)

## How it works in 30 seconds

1. Sender picks a file. Gets a passphrase like `RIVER FALCON MARBLE 42`.
2. Receiver types it at the homepage, runs `airpipe download <PHRASE>`, or scans the QR.
3. Both pair through the relay, then the file streams directly between them over WebRTC.
4. If the receiver isn't online yet, the sender can pick "mailbox" mode instead. The relay holds the encrypted file for 10 minutes.

Same passphrase works for both modes. Receiver doesn't need to know which one the sender chose.

## Self-host

```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
```

That's it. One Go binary, ~15 MB image. Bundles the landing page, browser sender/receiver, and the install script.

Or with the bundled `docker-compose.yml` (includes an opt-in Watchtower sidecar that auto-pulls new images):

```bash
git clone https://github.com/Sanyam-G/Airpipe
cd Airpipe
docker compose up -d
```

Point the CLI at your relay (per-call):

```bash
airpipe --relay https://your-server.example send file.txt
```

Or set it permanently:

```bash
export AIRPIPE_RELAY=https://your-server.example
airpipe send file.txt
```

Env vars if you want to tune things: `PORT` (default `8080`), `AIRPIPE_ALLOWED_ORIGINS` (CORS - set to domain(s) you host from, default `"http://localhost:8080","http://127.0.0.1:8080"` (`*` for any origin)), `AIRPIPE_RATE_LIMIT_PER_MIN` (default `60`), `AIRPIPE_LOG_FORMAT` (`json` or `text`).

## CLI

Install:
```bash
curl -sSL https://airpipe.sanyamgarg.com/install.sh | sh
```

Or via Go:
```bash
go install github.com/Sanyam-G/Airpipe/cmd/airpipe@latest
```

Self-update later: `airpipe update`. Linux + macOS, amd64 + arm64.

### Send

```bash
airpipe send report.pdf
```
You get a prompt: direct (P2P) or mailbox (relay holds it 10 min). Pick one. The CLI shows a passphrase, a QR, and a link.

### Download

```bash
airpipe download RIVER FALCON MARBLE 42
```

Multiple files or a folder get auto-zipped:
```bash
airpipe send file1.txt photos/
```

### Wait for someone to send to you

```bash
airpipe receive ./downloads
```
Prints a QR. Phone scans it, drops a file, the file lands in `./downloads`. Direct WebRTC, fallback to relay if NAT punching fails.

## Browser to browser, no install

Open `airpipe.sanyamgarg.com/live`. Get a passphrase + QR. Receiver types the passphrase at the homepage in their browser. Both pair, sender drops a file. No CLI, no app, no account.

## Encryption

NaCl secretbox (Poly1305 + XSalsa20, 256-bit key) on top of DTLS for the live path. The key never leaves the side that generated it. The relay only sees a 16-char room token and ciphertext.

The passphrase derives both the relay token and the encryption key via SHA-256 with domain separation. Same algorithm on CLI and browser.

## Stack

Go relay (gorilla/websocket, pion/webrtc), embedded HTML/CSS/JS frontend (tweetnacl.js for browser crypto), Docker, Cloudflare Tunnel optional. Single static binary.

## License

MIT
