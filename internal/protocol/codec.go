package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	LengthFieldSize = 4
	TypeFieldSize   = 2
	MaxPacketSize   = 1 << 20
)

var (
	ErrPacketTooLarge = errors.New("protocol: packet exceeds max size")
	ErrPacketTooSmall = errors.New("protocol: packet smaller than header")
)

func Encode(msgType uint16, body []byte) ([]byte, error) {
	length := TypeFieldSize + len(body)
	if length > MaxPacketSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrPacketTooLarge, length)
	}
	buf := make([]byte, LengthFieldSize)
	binary.BigEndian.PutUint32(buf[0:LengthFieldSize], uint32(length))
	binary.BigEndian.PutUint16(buf[LengthFieldSize:LengthFieldSize+TypeFieldSize], msgType)
	copy(buf[LengthFieldSize+TypeFieldSize:], body)
	return buf, nil
}

func ReadFrame(r io.Reader) (msgType uint16, body []byte, err error) {
	var lenBuf [LengthFieldSize]byte
	if _, err = io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length < TypeFieldSize {
		return 0, nil, ErrPacketTooSmall
	}
	if length > MaxPacketSize {
		return 0, nil, fmt.Errorf("%w: %d bytes", ErrPacketTooLarge, length)
	}
	payload := make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	msgType = binary.BigEndian.Uint16(payload[:TypeFieldSize])
	body = payload[TypeFieldSize:]
	return msgType, body, nil
}
