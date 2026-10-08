// Package satel implements the Satel Integra integration protocol
// (ETHM-1 Plus TCP, port 7094): frame encoding with CRC and byte stuffing,
// optional AES-192 encryption, and a Panel client with state polling,
// delta detection and async control commands.
package satel

import "math/bits"

// crcSeed is the initial CRC value defined by the Satel integration protocol.
const crcSeed uint16 = 0x147A

// crc16 computes the Satel frame checksum over cmd+data (unstuffed bytes).
func crc16(data []byte) uint16 {
	c := crcSeed
	for _, b := range data {
		c = bits.RotateLeft16(c, 1)
		c ^= 0xFFFF
		c = c + (c >> 8) + uint16(b)
	}
	return c
}
