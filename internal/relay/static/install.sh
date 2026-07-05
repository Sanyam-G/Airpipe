#!/bin/sh
set -e

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

RELAY="__RELAY_URL__"
URL="https://github.com/Sanyam-G/Airpipe/releases/latest/download/airpipe-${OS}-${ARCH}"

echo "Downloading airpipe for ${OS}-${ARCH}..."
curl -sL "$URL" -o /tmp/airpipe
chmod +x /tmp/airpipe

# Install to /usr/local/bin, use sudo if needed
if [ -w /usr/local/bin ]; then
    mv /tmp/airpipe /usr/local/bin/airpipe
    echo "Installed to /usr/local/bin/airpipe"
else
    echo "Need sudo to install to /usr/local/bin"
    sudo mv /tmp/airpipe /usr/local/bin/airpipe
    echo "Installed to /usr/local/bin/airpipe"
fi

echo "Done! Run: airpipe send <file>"

if [ "$RELAY" != "https://airpipe.sanyamgarg.com" ] && [ "$RELAY" != "__RELAY_URL__" ]; then
    case "$SHELL" in
        */zsh)  RC="$HOME/.zshrc" ;;
        */bash) RC="$HOME/.bashrc" ;;
        *)      RC="" ;;
    esac

    echo
    if [ -n "$RC" ] && [ -e /dev/tty ]; then
        printf "Add 'export AIRPIPE_RELAY=%s' to %s? [y/N] " "$RELAY" "$RC"
        read REPLY < /dev/tty
        case "$REPLY" in
            [yY]*)
                echo "export AIRPIPE_RELAY=$RELAY" >> "$RC"
                echo "Added. Restart your shell or run: export AIRPIPE_RELAY=$RELAY"
                exit 0
                ;;
        esac
    fi
    echo "To use this relay by default:"
    echo "  export AIRPIPE_RELAY=$RELAY"
fi
