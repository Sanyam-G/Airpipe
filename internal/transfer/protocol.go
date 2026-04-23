package transfer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

type MessageType byte

const (
	MsgTypeMetadata     MessageType = 0x01
	MsgTypeReady        MessageType = 0x02
	MsgTypeComplete     MessageType = 0x03
	MsgTypeError        MessageType = 0x04
	MsgTypeChunk        MessageType = 0x10
	MsgTypeProgress     MessageType = 0x11
	MsgTypeVersion      MessageType = 0x20
	MsgTypeSDPOffer     MessageType = 0x30
	MsgTypeSDPAnswer    MessageType = 0x31
	MsgTypeICECandidate MessageType = 0x32
	MsgTypeP2PReady     MessageType = 0x33
	MsgTypeP2PFail      MessageType = 0x34
)

// ProtocolVersion identifies the wire format. v1 is WS streaming through the
// relay. The SDP/ICE message types are defined for the upcoming v2 WebRTC path
// but are not yet triggered by default.
const ProtocolVersion byte = 1

type Metadata struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Chunks   int    `json:"chunks"`
}

type Progress struct {
	ChunkIndex  int   `json:"chunk_index"`
	TotalChunks int   `json:"total_chunks"`
	BytesSent   int64 `json:"bytes_sent"`
	TotalBytes  int64 `json:"total_bytes"`
}

type Message struct {
	Type    MessageType
	Payload []byte
}

func EncodeMessage(msg Message) []byte {
	result := make([]byte, 5+len(msg.Payload))
	result[0] = byte(msg.Type)
	binary.BigEndian.PutUint32(result[1:5], uint32(len(msg.Payload)))
	copy(result[5:], msg.Payload)
	return result
}

func DecodeMessage(data []byte) (Message, error) {
	if len(data) < 5 {
		return Message{}, errors.New("message too short")
	}
	msgType := MessageType(data[0])
	payloadLen := binary.BigEndian.Uint32(data[1:5])
	if len(data) < int(5+payloadLen) {
		return Message{}, errors.New("incomplete message")
	}
	return Message{
		Type:    msgType,
		Payload: data[5 : 5+payloadLen],
	}, nil
}

func NewMetadataMessage(filename string, size int64, chunks int) (Message, error) {
	meta := Metadata{Filename: filename, Size: size, Chunks: chunks}
	payload, err := json.Marshal(meta)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: MsgTypeMetadata, Payload: payload}, nil
}

func NewChunkMessage(data []byte) Message {
	return Message{Type: MsgTypeChunk, Payload: data}
}

func NewReadyMessage() Message {
	return Message{Type: MsgTypeReady, Payload: nil}
}

func NewCompleteMessage() Message {
	return Message{Type: MsgTypeComplete, Payload: nil}
}

func NewErrorMessage(errStr string) Message {
	return Message{Type: MsgTypeError, Payload: []byte(errStr)}
}

func NewVersionMessage() Message {
	return Message{Type: MsgTypeVersion, Payload: []byte{ProtocolVersion}}
}

func NewSDPOfferMessage(sdp string) Message {
	return Message{Type: MsgTypeSDPOffer, Payload: []byte(sdp)}
}

func NewSDPAnswerMessage(sdp string) Message {
	return Message{Type: MsgTypeSDPAnswer, Payload: []byte(sdp)}
}

func NewICECandidateMessage(candidate []byte) Message {
	return Message{Type: MsgTypeICECandidate, Payload: candidate}
}

func NewP2PReadyMessage() Message {
	return Message{Type: MsgTypeP2PReady, Payload: nil}
}

func NewP2PFailMessage(reason string) Message {
	return Message{Type: MsgTypeP2PFail, Payload: []byte(reason)}
}

func ParseMetadata(payload []byte) (Metadata, error) {
	var meta Metadata
	err := json.Unmarshal(payload, &meta)
	return meta, err
}
