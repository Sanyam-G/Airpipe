# AirPipe

Encrypted file transfer. WebRTC peer-to-peer with relay fallback. No accounts. No apps.

```
$ airpipe send config.yaml

  ╔══════════════════════════════════════════╗
  ║  RIVER FALCON MARBLE 42                  ║
  ╚══════════════════════════════════════════╝

  Tell them: airpipe.sanyamgarg.com
  They type the code, they get the file.
```

**Try it:** [airpipe.sanyamgarg.com](https://airpipe.sanyamgarg.com)

![demo](demo.gif)

## Self-host

One container. Bundles the relay, landing page, browser sender, download pages, and the install script.

```bash
docker run -p 8080:8080 ghcr.io/sanyam-g/airpipe-relay
```

Or with the bundled `docker-compose.yml` (includes an opt-in Watchtower sidecar that auto-pulls new images):

```bash
git clone https://github.com/Sanyam-G/Airpipe
cd Airpipe
docker compose up -d
```

Point the CLI at your relay:

```bash
airpipe --relay https://your-server.example send file.txt
```

### Config

| Env var | Purpose | Default |
|---|---|---|
| `PORT` | Listen port | `8080` |
| `AIRPIPE_ALLOWED_ORIGINS` | CORS allowlist for browsers, CSV or `*` | `*` |
| `AIRPIPE_RATE_LIMIT_PER_MIN` | Per-IP token bucket | `60` |
| `AIRPIPE_LOG_FORMAT` | `json` or `text` | `json` |

`/health` returns JSON: `version`, `uptime_seconds`, `active_files`, `active_bytes`, `active_ws_rooms`, `protocol_version`.

## Install the CLI

```bash
curl -sSL https://airpipe.sanyamgarg.com/install.sh | sh
```

Or via Go:
```bash
go install github.com/Sanyam-G/Airpipe/cmd/airpipe@latest
```

Self-update:
```bash
airpipe update
```

Linux + macOS, amd64 + arm64.

## Usage

**Send a file** (passphrase flow):
```bash
airpipe send photo.jpg
```
Shows a passphrase and a QR code. The receiver either:
- Types the passphrase at the relay homepage
- Scans the QR
- Runs `airpipe download RIVER FALCON MARBLE 42` in their terminal

**Download:**
```bash
airpipe download RIVER FALCON MARBLE 42
```

**Send multiple files / a directory** (auto-zipped):
```bash
airpipe send file1.txt photos/
```

**Receive from a browser** (CLI side waits for a phone or laptop to drop a file in):
```bash
airpipe receive ./downloads
```
Prints a QR. Phone scans it, drops a file. WebRTC direct, fallback to relay if NAT punching fails.

**Browser to browser, no CLI:** open `/live` on the relay in two tabs. The first generates a passphrase + QR; the second joins by URL. Same WebRTC + fallback path, no install on either side.

## How it works

Two transports, one passphrase-derived key.

**Async flow** (`airpipe send` / `airpipe download`, or `/send` in the browser):
file is encrypted locally with NaCl secretbox and uploaded to the relay as ciphertext. The receiver derives the same key from the passphrase and decrypts locally. The relay sees the ciphertext and a 16-char hex token, nothing else.

**Live flow** (CLI ↔ browser, or browser ↔ browser via `/live`):
both peers connect to the relay's WebSocket room. They exchange WebRTC SDP and ICE candidates through the room, all encrypted with the passphrase-derived key so the relay can't impersonate either side. When the DataChannel opens, file bytes flow directly between the peers. If NAT punching fails after 15 seconds, both sides fall back to streaming through the WebSocket. The relay still only sees ciphertext.

Two layers of encryption: DTLS (built into WebRTC) protects the wire; NaCl secretbox sits on top so even a malicious relay can't impersonate peers in the SDP exchange.

Files expire after 10 minutes.

## Stack

Go 1.22+ relay (gorilla/websocket, pion/webrtc), embedded HTML/CSS/JS frontend (tweetnacl.js for browser crypto), Docker, Cloudflare Tunnel optional. Single static binary, ~15 MB image.

## License

MIT
