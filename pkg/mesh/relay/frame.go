package relay

import (
	"crypto/subtle"
	"errors"
)

const (
	// HeaderSize is the plaintext routing envelope added outside every QUIC
	// datagram. The bytes after this header remain opaque QUIC ciphertext.
	HeaderSize = 4 + 1 + tokenSize
	maxUDPSize = 64 * 1024
)

var frameMagic = [4]byte{'S', 'Y', 'R', 1}

// Side identifies one half of a relay session. Names describe SoyaOS roles,
// not trust levels: both sides must present the same signed token.
type Side byte

const (
	SideMoon Side = iota
	SideComet
)

var errInvalidFrame = errors.New("relay: invalid datagram envelope")

func (s Side) valid() bool { return s == SideMoon || s == SideComet }

func (s Side) other() Side {
	if s == SideMoon {
		return SideComet
	}
	return SideMoon
}

func encodeFrame(token Token, side Side, payload []byte) []byte {
	frame := make([]byte, HeaderSize+len(payload))
	copy(frame[:4], frameMagic[:])
	frame[4] = byte(side)
	copy(frame[5:HeaderSize], token.raw[:])
	copy(frame[HeaderSize:], payload)
	return frame
}

func decodeFrame(frame []byte) (Token, Side, []byte, error) {
	if len(frame) < HeaderSize || len(frame) > maxUDPSize {
		return Token{}, 0, nil, errInvalidFrame
	}
	if subtle.ConstantTimeCompare(frame[:4], frameMagic[:]) != 1 {
		return Token{}, 0, nil, errInvalidFrame
	}
	side := Side(frame[4])
	if !side.valid() {
		return Token{}, 0, nil, errInvalidFrame
	}
	var token Token
	copy(token.raw[:], frame[5:HeaderSize])
	return token, side, frame[HeaderSize:], nil
}
