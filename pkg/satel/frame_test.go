package satel

import (
	"bytes"
	"reflect"
	"testing"
)

// CRC test vectors from the Satel docs and public implementations
// (node-red satel-integra-integration-protocol crc.test.js, c-soft tests).
func TestCRC16(t *testing.T) {
	cases := []struct {
		data []byte
		want uint16
	}{
		{[]byte{0xE0, 0x12, 0x34, 0xFF, 0xFF}, 0x8A9B}, // official doc example
		{[]byte{0x09}, 0xD7EB},
		{[]byte{0x00}, 0xD7E2},
		{[]byte{0x09, 0xFE}, 0x5176},
		{[]byte{0x1C}, 0xD7FE},
		{[]byte{0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 0xFE8A},
	}
	for _, c := range cases {
		if got := crc16(c.data); got != c.want {
			t.Errorf("crc16(% X) = 0x%04X, want 0x%04X", c.data, got, c.want)
		}
	}
}

func TestEncodeFrame(t *testing.T) {
	cases := []struct {
		name string
		cmd  byte
		data []byte
		want []byte
	}{
		{"doc example", 0xE0, []byte{0x12, 0x34, 0xFF, 0xFF},
			[]byte{0xFE, 0xFE, 0xE0, 0x12, 0x34, 0xFF, 0xFF, 0x8A, 0x9B, 0xFE, 0x0D}},
		{"no data", 0x09, nil,
			[]byte{0xFE, 0xFE, 0x09, 0xD7, 0xEB, 0xFE, 0x0D}},
		{"stuffing in data", 0x09, []byte{0xFE},
			[]byte{0xFE, 0xFE, 0x09, 0xFE, 0xF0, 0x51, 0x76, 0xFE, 0x0D}},
		{"stuffing in CRC", 0x1C, nil,
			[]byte{0xFE, 0xFE, 0x1C, 0xD7, 0xFE, 0xF0, 0xFE, 0x0D}},
		{"monitoring frame", 0x7F, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			[]byte{0xFE, 0xFE, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE, 0xF0, 0x8A, 0xFE, 0x0D}},
	}
	for _, c := range cases {
		if got := EncodeFrame(c.cmd, c.data); !bytes.Equal(got, c.want) {
			t.Errorf("%s: EncodeFrame = % X, want % X", c.name, got, c.want)
		}
	}
}

func TestDecoderRoundTrip(t *testing.T) {
	var d frameDecoder
	wire := append([]byte{}, EncodeFrame(0xE0, []byte{0x12, 0x34, 0xFF, 0xFF})...)
	wire = append(wire, EncodeFrame(0x09, []byte{0xFE})...)
	wire = append(wire, EncodeFrame(0x1C, nil)...)

	frames, err := d.Feed(wire)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	want := []Frame{
		{Cmd: 0xE0, Data: []byte{0x12, 0x34, 0xFF, 0xFF}},
		{Cmd: 0x09, Data: []byte{0xFE}},
		{Cmd: 0x1C, Data: []byte{}},
	}
	if !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %+v, want %+v", frames, want)
	}
}

func TestDecoderByteByByte(t *testing.T) {
	var d frameDecoder
	wire := EncodeFrame(0x7F, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	var frames []Frame
	for _, b := range wire {
		fs, err := d.Feed([]byte{b})
		if err != nil {
			t.Fatalf("Feed error: %v", err)
		}
		frames = append(frames, fs...)
	}
	if len(frames) != 1 || frames[0].Cmd != 0x7F || len(frames[0].Data) != 6 {
		t.Errorf("frames = %+v", frames)
	}
}

func TestDecoderResync(t *testing.T) {
	var d frameDecoder
	// Garbage, then a partial frame interrupted by FE FE restart, then a valid frame.
	wire := []byte{0x00, 0x42, 0xFE, 0x01}         // garbage incl. FE + non-sync
	wire = append(wire, 0xFE, 0xFE, 0xE0, 0x12)    // partial frame...
	wire = append(wire, EncodeFrame(0x09, nil)...) // ...restarted by this frame's FE FE
	// Trailing zero padding (as after encrypted PDU decryption)
	wire = append(wire, 0x00, 0x00, 0x00)

	frames, err := d.Feed(wire)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 1 || frames[0].Cmd != 0x09 {
		t.Errorf("frames = %+v, want single 0x09 frame", frames)
	}
}

func TestDecoderBadCRC(t *testing.T) {
	var d frameDecoder
	wire := EncodeFrame(0x09, nil)
	wire[2] = 0x0A // corrupt cmd byte, CRC no longer matches
	frames, err := d.Feed(wire)
	if err == nil {
		t.Error("expected CRC error")
	}
	if len(frames) != 0 {
		t.Errorf("expected no frames, got %+v", frames)
	}
	// Decoder must keep working after a bad frame.
	frames, err = d.Feed(EncodeFrame(0x09, nil))
	if err != nil || len(frames) != 1 {
		t.Errorf("decoder did not recover: frames=%+v err=%v", frames, err)
	}
}

func TestEncodeUserCode(t *testing.T) {
	cases := []struct {
		code string
		want [8]byte
	}{
		{"0", [8]byte{0x0F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"0000", [8]byte{0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"123456", [8]byte{0x12, 0x34, 0x56, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"98124", [8]byte{0x98, 0x12, 0x4F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
	}
	for _, c := range cases {
		got, err := EncodeUserCode(c.code)
		if err != nil {
			t.Errorf("EncodeUserCode(%q) error: %v", c.code, err)
			continue
		}
		if got != c.want {
			t.Errorf("EncodeUserCode(%q) = % X, want % X", c.code, got, c.want)
		}
	}
	if _, err := EncodeUserCode("12a4"); err == nil {
		t.Error("expected error for non-digit code")
	}
	if _, err := EncodeUserCode("12345678901234567"); err == nil {
		t.Error("expected error for too long code")
	}
}

func TestMaskHelpers(t *testing.T) {
	mask, err := numbersToMask(4, 1, 9, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mask, []byte{0x01, 0x01, 0x00, 0x80}) {
		t.Errorf("mask = % X", mask)
	}
	if !maskBit(mask, 1) || !maskBit(mask, 9) || !maskBit(mask, 32) || maskBit(mask, 2) {
		t.Error("maskBit mismatch")
	}
	if maskBit(mask, 0) || maskBit(mask, 33) {
		t.Error("out-of-range maskBit should be false")
	}
	if _, err := numbersToMask(4, 33); err == nil {
		t.Error("expected range error")
	}

	changed := diffMask([]byte{0x01, 0x00}, []byte{0x00, 0x81})
	if !reflect.DeepEqual(changed, []int{1, 9, 16}) {
		t.Errorf("diffMask = %v", changed)
	}
	if got := diffMask(nil, []byte{0x03}); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("diffMask from nil = %v", got)
	}
}

func TestParseRange(t *testing.T) {
	got, err := ParseRange("1-4,10, 12, 3-5")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 10, 12}) {
		t.Errorf("ParseRange = %v", got)
	}
	if got, _ := ParseRange(""); len(got) != 0 {
		t.Errorf("empty spec should give empty list, got %v", got)
	}
	for _, bad := range []string{"a", "5-2", "0", "-3", "1-"} {
		if _, err := ParseRange(bad); err == nil {
			t.Errorf("ParseRange(%q): expected error", bad)
		}
	}
}
