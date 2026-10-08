package satel

import (
	"errors"
	"fmt"
)

const (
	frameSync    byte = 0xFE
	frameSyncEsc byte = 0xF0
	frameEnd     byte = 0x0D
)

// Frame is a decoded protocol frame: command byte plus payload.
type Frame struct {
	Cmd  byte
	Data []byte
}

// ErrBadCRC is returned when a received frame fails checksum validation.
var ErrBadCRC = errors.New("satel: frame CRC mismatch")

// EncodeFrame builds a wire frame: FE FE <stuffed cmd+data+crc> FE 0D.
// CRC is computed over unstuffed cmd+data; stuffing (FE -> FE F0) is applied
// to cmd, data and CRC bytes, never to the header/footer markers.
func EncodeFrame(cmd byte, data []byte) []byte {
	payload := make([]byte, 0, len(data)+3)
	payload = append(payload, cmd)
	payload = append(payload, data...)
	c := crc16(payload)
	payload = append(payload, byte(c>>8), byte(c))

	out := make([]byte, 0, len(payload)+8)
	out = append(out, frameSync, frameSync)
	for _, b := range payload {
		out = append(out, b)
		if b == frameSync {
			out = append(out, frameSyncEsc)
		}
	}
	out = append(out, frameSync, frameEnd)
	return out
}

// frameDecoder is a streaming decoder for the Satel frame format.
// Feed it raw bytes; it emits complete, CRC-validated frames and silently
// resynchronizes on garbage (per the openHAB/node-red state machine):
//
//	FE F0          -> literal 0xFE
//	FE 0D          -> end of frame
//	FE FE mid-data -> restart frame (resync)
//	FE <other>     -> discard, look for new FE FE
type frameDecoder struct {
	buf     []byte
	inFrame bool
	sawSync bool
}

// Feed consumes raw stream bytes and returns any complete frames.
// Frames with bad CRC are dropped and reported via the error (decoding continues).
func (d *frameDecoder) Feed(p []byte) ([]Frame, error) {
	var frames []Frame
	var firstErr error

	for _, b := range p {
		if !d.inFrame {
			if d.sawSync && b == frameSync {
				d.inFrame = true
				d.sawSync = false
				d.buf = d.buf[:0]
				continue
			}
			d.sawSync = b == frameSync
			continue
		}

		if d.sawSync {
			d.sawSync = false
			switch b {
			case frameSyncEsc:
				d.buf = append(d.buf, frameSync)
			case frameEnd:
				if f, err := d.finish(); err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					frames = append(frames, f)
				}
				d.inFrame = false
				d.buf = d.buf[:0]
			case frameSync:
				// FE FE mid-frame: restart frame from scratch
				d.buf = d.buf[:0]
			default:
				// Protocol violation: drop buffer, resync on next FE FE
				d.inFrame = false
				d.buf = d.buf[:0]
			}
			continue
		}

		if b == frameSync {
			d.sawSync = true
			continue
		}
		d.buf = append(d.buf, b)
	}

	return frames, firstErr
}

// finish validates CRC of the unstuffed buffer and builds the Frame.
func (d *frameDecoder) finish() (Frame, error) {
	if len(d.buf) < 3 {
		return Frame{}, fmt.Errorf("satel: frame too short (%d bytes)", len(d.buf))
	}
	body := d.buf[:len(d.buf)-2]
	got := uint16(d.buf[len(d.buf)-2])<<8 | uint16(d.buf[len(d.buf)-1])
	if crc16(body) != got {
		return Frame{}, ErrBadCRC
	}
	data := make([]byte, len(body)-1)
	copy(data, body[1:])
	return Frame{Cmd: body[0], Data: data}, nil
}
