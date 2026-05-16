// Package mailbox defines the ciphertext-before-encryption payloads for relay mailbox mode.
package mailbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	magicAMB2       = "AMB2"
	MaxFilesPerSend = 256
	MaxFilenameLen  = 4095
	MaxFilePayload  = 500 << 20 // must stay within relay max upload after encryption overhead
)

// Entry is one file inside an AMB2 mailbox blob (plaintext before Encrypt).
type Entry struct {
	Name    string
	Content []byte
}

var ErrInvalidPayload = errors.New("mailbox: invalid decrypted payload")

// EncodeV1 is the legacy mailbox layout: fnLen BE32 | UTF-8 name | raw contents.
func EncodeV1(filename string, content []byte) ([]byte, error) {
	fn := []byte(filename)
	if len(fn) > MaxFilenameLen {
		return nil, fmt.Errorf("mailbox: filename too long (%d)", len(fn))
	}
	if int64(len(content)) > MaxFilePayload {
		return nil, fmt.Errorf("mailbox: file too large")
	}
	buf := bytes.Buffer{}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(fn))); err != nil {
		return nil, err
	}
	buf.Write(fn)
	buf.Write(content)
	return buf.Bytes(), nil
}

// EncodeAMB2 bundles multiple entries as one plaintext before Encrypt.
func EncodeAMB2(entries []Entry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("mailbox: no entries")
	}
	if len(entries) > MaxFilesPerSend {
		return nil, fmt.Errorf("mailbox: at most %d files", MaxFilesPerSend)
	}
	var buf bytes.Buffer
	buf.WriteString(magicAMB2)
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(entries))); err != nil {
		return nil, err
	}
	var totalPayload int64
	for _, e := range entries {
		fn := []byte(e.Name)
		if len(fn) > MaxFilenameLen {
			return nil, fmt.Errorf("mailbox: filename too long in %q", e.Name)
		}
		if int64(len(e.Content)) > MaxFilePayload {
			return nil, fmt.Errorf("mailbox: file too large: %s", e.Name)
		}
		totalPayload += int64(len(e.Content))
		if totalPayload > MaxFilePayload {
			return nil, fmt.Errorf("mailbox: combined size too large")
		}
		if err := binary.Write(&buf, binary.BigEndian, uint32(len(fn))); err != nil {
			return nil, err
		}
		buf.Write(fn)
		if err := binary.Write(&buf, binary.BigEndian, uint64(len(e.Content))); err != nil {
			return nil, err
		}
		buf.Write(e.Content)
	}
	return buf.Bytes(), nil
}

// Decode returns one entry for legacy payloads or all entries for AMB2.
func Decode(decryptedPlaintext []byte) ([]Entry, error) {
	if len(decryptedPlaintext) < 4 {
		return nil, ErrInvalidPayload
	}
	if string(decryptedPlaintext[:4]) == magicAMB2 {
		return decodeAMB2(decryptedPlaintext[4:])
	}
	return decodeLegacyV1(decryptedPlaintext)
}

func decodeAMB2(rest []byte) ([]Entry, error) {
	if len(rest) < 4 {
		return nil, ErrInvalidPayload
	}
	n := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if n == 0 || n > MaxFilesPerSend {
		return nil, ErrInvalidPayload
	}
	out := make([]Entry, 0, n)
	for i := uint32(0); i < n; i++ {
		if len(rest) < 4 {
			return nil, ErrInvalidPayload
		}
		fnLen := int(binary.BigEndian.Uint32(rest[:4]))
		rest = rest[4:]
		if fnLen < 1 || fnLen > MaxFilenameLen || len(rest) < fnLen+8 {
			return nil, ErrInvalidPayload
		}
		name := string(rest[:fnLen])
		rest = rest[fnLen:]
		payloadLen := int(binary.BigEndian.Uint64(rest[:8]))
		rest = rest[8:]
		if payloadLen < 0 || len(rest) < payloadLen {
			return nil, ErrInvalidPayload
		}
		content := append([]byte(nil), rest[:payloadLen]...)
		rest = rest[payloadLen:]
		out = append(out, Entry{Name: name, Content: content})
	}
	if len(rest) != 0 {
		return nil, ErrInvalidPayload
	}
	return out, nil
}

func decodeLegacyV1(plaintext []byte) ([]Entry, error) {
	if len(plaintext) < 4 {
		return nil, ErrInvalidPayload
	}
	fnLen := int(binary.BigEndian.Uint32(plaintext[:4]))
	if fnLen <= 0 || fnLen > MaxFilenameLen || len(plaintext) < 4+fnLen {
		return nil, ErrInvalidPayload
	}
	name := string(plaintext[4 : 4+fnLen])
	content := plaintext[4+fnLen:]
	return []Entry{{Name: name, Content: append([]byte(nil), content...)}}, nil
}