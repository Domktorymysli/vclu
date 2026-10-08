package satel

import (
	"bytes"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	cases := []struct {
		password string
		want     string
	}{
		{"", "                        "},
		{"short_key", "short_key   short_key   "},
		{"too_long_key1234", "too_long_keytoo_long_key"},
	}
	for _, c := range cases {
		if got := string(deriveKey(c.password)); got != c.want {
			t.Errorf("deriveKey(%q) = %q, want %q", c.password, got, c.want)
		}
	}
}

// End-to-end encryption vector from satel_integra2/c-soft test_encryption.py
// (key "some_key"): full PDU (6-byte header + stuffed 0x7F monitoring frame).
func TestEncryptBufferVector(t *testing.T) {
	e, err := newEncrypter("some_key")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte{
		0x52, 0x3d, 0x00, 0x03, 0x00, 0x54, // header
		0xfe, 0xfe, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe, 0xf0, 0x8a, 0xfe, 0x0d,
	}
	want := []byte{
		0x43, 0xd8, 0x64, 0x30, 0x7e, 0x6b, 0x84, 0x31, 0xcd, 0x75,
		0x59, 0xce, 0x2e, 0xf9, 0xd4, 0x1f, 0x26, 0x61, 0x5c, 0x2f,
	}

	buf := append([]byte{}, plain...)
	e.encryptBuffer(buf)
	if !bytes.Equal(buf, want) {
		t.Errorf("encryptBuffer = % x\nwant           = % x", buf, want)
	}

	e.decryptBuffer(buf)
	if !bytes.Equal(buf, plain) {
		t.Errorf("decryptBuffer = % x\nwant          = % x", buf, plain)
	}
}

// A short message zero-padded to 16 bytes must round-trip with the padding.
func TestEncryptShortMessageRoundTrip(t *testing.T) {
	e, err := newEncrypter("some_key")
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("0123456789abc") // 13 bytes
	buf := make([]byte, 16)
	copy(buf, msg)
	e.encryptBuffer(buf)
	e.decryptBuffer(buf)
	want := append(append([]byte{}, msg...), 0x00, 0x00, 0x00)
	if !bytes.Equal(buf, want) {
		t.Errorf("round trip = % x, want % x", buf, want)
	}
}

// wrap/unwrap between two peers: headers, rolling counter and id echo.
func TestWrapUnwrapSession(t *testing.T) {
	client, _ := newEncrypter("integr_key")
	server, _ := newEncrypter("integr_key")

	for i := 0; i < 3; i++ {
		// Client -> server
		frame := EncodeFrame(0x09, nil)
		pdu, err := client.wrap(frame)
		if err != nil {
			t.Fatal(err)
		}
		if int(pdu[0]) != len(pdu)-1 {
			t.Fatalf("length byte %d != %d", pdu[0], len(pdu)-1)
		}
		got, err := server.unwrap(pdu[1:])
		if err != nil {
			t.Fatalf("server unwrap (round %d): %v", i, err)
		}
		if !bytes.Equal(got[:len(frame)], frame) {
			t.Fatalf("server got % x, want % x", got[:len(frame)], frame)
		}

		// Server -> client (response)
		resp := EncodeFrame(0x09, []byte{0x01, 0x00, 0x00, 0x00})
		pdu, err = server.wrap(resp)
		if err != nil {
			t.Fatal(err)
		}
		got, err = client.unwrap(pdu[1:])
		if err != nil {
			t.Fatalf("client unwrap (round %d): %v", i, err)
		}
		if !bytes.Equal(got[:len(resp)], resp) {
			t.Fatalf("client got % x, want % x", got[:len(resp)], resp)
		}
	}
}

// Wrong key must be detected via the id_s echo check.
func TestUnwrapWrongKey(t *testing.T) {
	client, _ := newEncrypter("right_key")
	server, _ := newEncrypter("wrong_key")

	// Establish a nonzero id_s on the client so the echo check is meaningful.
	pdu, err := client.wrap(EncodeFrame(0x09, nil))
	if err != nil {
		t.Fatal(err)
	}
	if client.idS == 0 {
		client.idS = 1 // wrap randomizes; force nonzero for a deterministic test
	}
	resp, err := server.wrap(EncodeFrame(0x09, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = pdu
	inner, err := client.unwrap(resp[1:])
	if err == nil {
		// 1-in-256 chance the garbage id byte matches — the CRC layer must
		// then reject the garbage frame.
		var d frameDecoder
		frames, _ := d.Feed(inner)
		if len(frames) > 0 {
			t.Error("expected wrong key to be detected (id echo or CRC)")
		}
	}
}
