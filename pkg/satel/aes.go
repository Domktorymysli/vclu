package satel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// encrypter implements the ETHM-1 Plus "integration encryption":
// AES-192 with a custom CBC mode for full blocks and an OFB-style keystream
// for the trailing partial block. Each PDU on the wire is prefixed with one
// length byte and carries a 6-byte header before the byte-stuffed frame:
//
//	bytes 0-1: random
//	bytes 2-3: rolling counter (big-endian, incremented per sent PDU)
//	byte  4:   id_s (sender id, regenerated per message)
//	byte  5:   id_r (echo of the peer's last id_s)
type encrypter struct {
	block   cipher.Block
	rolling uint16
	idS     byte
	idR     byte
}

// deriveKey expands the integration password into a 24-byte AES-192 key:
// pad/truncate to 12 bytes with spaces, then duplicate.
func deriveKey(password string) []byte {
	key := make([]byte, 24)
	for i := 0; i < 12; i++ {
		if i < len(password) {
			key[i] = password[i]
		} else {
			key[i] = ' '
		}
		key[i+12] = key[i]
	}
	return key
}

// newEncrypter creates an encrypter for the given integration password.
func newEncrypter(password string) (*encrypter, error) {
	block, err := aes.NewCipher(deriveKey(password))
	if err != nil {
		return nil, fmt.Errorf("satel: aes init: %w", err)
	}
	return &encrypter{block: block}, nil
}

// initialCV returns the chaining value: AES-ECB encryption of the zero block.
func (e *encrypter) initialCV() []byte {
	cv := make([]byte, aes.BlockSize)
	e.block.Encrypt(cv, cv)
	return cv
}

// encryptBuffer encrypts buf in place: CBC for full blocks (IV = E(zeros)),
// OFB keystream for the trailing partial block.
func (e *encrypter) encryptBuffer(buf []byte) {
	cv := e.initialCV()
	i := 0
	for ; i+aes.BlockSize <= len(buf); i += aes.BlockSize {
		blk := buf[i : i+aes.BlockSize]
		for j := range blk {
			blk[j] ^= cv[j]
		}
		e.block.Encrypt(blk, blk)
		copy(cv, blk)
	}
	if tail := buf[i:]; len(tail) > 0 {
		e.block.Encrypt(cv, cv)
		for j := range tail {
			tail[j] ^= cv[j]
		}
	}
}

// decryptBuffer decrypts buf in place (mirror of encryptBuffer; the partial
// tail uses the encrypting direction of AES, as in OFB).
func (e *encrypter) decryptBuffer(buf []byte) {
	cv := e.initialCV()
	temp := make([]byte, aes.BlockSize)
	i := 0
	for ; i+aes.BlockSize <= len(buf); i += aes.BlockSize {
		blk := buf[i : i+aes.BlockSize]
		copy(temp, blk)
		e.block.Decrypt(blk, blk)
		for j := range blk {
			blk[j] ^= cv[j]
		}
		copy(cv, temp)
	}
	if tail := buf[i:]; len(tail) > 0 {
		e.block.Encrypt(cv, cv)
		for j := range tail {
			tail[j] ^= cv[j]
		}
	}
}

// wrap builds a wire PDU for a frame: 6-byte header + frame, zero-padded to a
// minimum of 16 bytes, encrypted, prefixed with a single length byte.
func (e *encrypter) wrap(frame []byte) ([]byte, error) {
	size := 6 + len(frame)
	if size < aes.BlockSize {
		size = aes.BlockSize
	}
	if size > 255 {
		return nil, fmt.Errorf("satel: frame too long for encrypted PDU (%d bytes)", size)
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf[:2]); err != nil {
		return nil, err
	}
	buf[2] = byte(e.rolling >> 8)
	buf[3] = byte(e.rolling)
	e.rolling++
	var idBuf [1]byte
	if _, err := rand.Read(idBuf[:]); err != nil {
		return nil, err
	}
	e.idS = idBuf[0]
	buf[4] = e.idS
	buf[5] = e.idR
	copy(buf[6:], frame)

	e.encryptBuffer(buf)
	return append([]byte{byte(len(buf))}, buf...), nil
}

// unwrap decrypts a received PDU body (without the length byte), validates the
// id echo and returns the inner frame bytes (may include trailing zero padding,
// which the frame decoder ignores).
func (e *encrypter) unwrap(pdu []byte) ([]byte, error) {
	if len(pdu) < aes.BlockSize {
		return nil, fmt.Errorf("satel: encrypted PDU too short (%d bytes)", len(pdu))
	}
	buf := make([]byte, len(pdu))
	copy(buf, pdu)
	e.decryptBuffer(buf)

	e.idR = buf[4]
	if buf[5] != e.idS {
		return nil, fmt.Errorf("satel: id mismatch (got 0x%02X, expected 0x%02X) — wrong integration key?", buf[5], e.idS)
	}
	return buf[6:], nil
}
