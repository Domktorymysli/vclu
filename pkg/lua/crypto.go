package lua

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// Crypto and bit: pure functions for modules and plugins (#257). Binary data is a plain Lua
// string; errors in the arguments are raised at the caller's line.

const gcmNonceSize = 12

var cryptoHashes = map[string]func() hash.Hash{
	"sha256": sha256.New,
	"sha1":   sha1.New,
	"md5":    md5.New,
}

func registerCryptoFunctions(L *lua.LState) {
	c := L.NewTable()
	for name, fn := range map[string]lua.LGFunction{
		"sha256":        cryptoDigest("sha256"),
		"sha1":          cryptoDigest("sha1"),
		"md5":           cryptoDigest("md5"),
		"hmac":          cryptoHmac,
		"aesGcmEncrypt": cryptoGcmEncrypt,
		"aesGcmDecrypt": cryptoGcmDecrypt,
		"aesEcbEncrypt": cryptoBlockMode("aesEcbEncrypt", false, true),
		"aesEcbDecrypt": cryptoBlockMode("aesEcbDecrypt", false, false),
		"aesCbcEncrypt": cryptoBlockMode("aesCbcEncrypt", true, true),
		"aesCbcDecrypt": cryptoBlockMode("aesCbcDecrypt", true, false),
		"random":        cryptoRandom,
		"toHex":         cryptoToHex,
		"fromHex":       cryptoFromHex,
		"toBase64":      cryptoToBase64,
		"fromBase64":    cryptoFromBase64,
	} {
		L.SetField(c, name, L.NewFunction(fn))
	}
	L.SetGlobal("Crypto", c)

	b := L.NewTable()
	for name, fn := range map[string]lua.LGFunction{
		"band":    bitFold(func(a, b int32) int32 { return a & b }),
		"bor":     bitFold(func(a, b int32) int32 { return a | b }),
		"bxor":    bitFold(func(a, b int32) int32 { return a ^ b }),
		"bnot":    func(L *lua.LState) int { return pushBit(L, ^checkBit(L, 1)) },
		"lshift":  bitShift(func(x int32, n uint) int32 { return int32(uint32(x) << n) }),
		"rshift":  bitShift(func(x int32, n uint) int32 { return int32(uint32(x) >> n) }),
		"arshift": bitShift(func(x int32, n uint) int32 { return x >> n }),
		"tohex":   bitToHex,
	} {
		L.SetField(b, name, L.NewFunction(fn))
	}
	L.SetGlobal("bit", b)
}

func cryptoDigest(alg string) lua.LGFunction {
	return func(L *lua.LState) int {
		h := cryptoHashes[alg]()
		h.Write([]byte(L.CheckString(1)))
		L.Push(lua.LString(h.Sum(nil)))
		return 1
	}
}

func cryptoHmac(L *lua.LState) int {
	alg := L.CheckString(1)
	newHash, ok := cryptoHashes[alg]
	if !ok {
		L.RaiseError("Crypto.hmac: alg must be \"sha256\", \"sha1\" or \"md5\", got %q", alg)
	}
	m := hmac.New(newHash, []byte(L.CheckString(2)))
	m.Write([]byte(L.CheckString(3)))
	L.Push(lua.LString(m.Sum(nil)))
	return 1
}

func checkAesKey(L *lua.LState, fn string, n int) cipher.Block {
	key := L.CheckString(n)
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		L.RaiseError("Crypto.%s: key must be 16, 24 or 32 bytes, got %d", fn, len(key))
	}
	return block
}

func checkGcm(L *lua.LState, fn string) (cipher.AEAD, []byte, []byte, []byte) {
	block := checkAesKey(L, fn, 1)
	nonce := L.CheckString(2)
	if len(nonce) != gcmNonceSize {
		L.RaiseError("Crypto.%s: nonce must be %d bytes, got %d", fn, gcmNonceSize, len(nonce))
	}
	data := L.CheckString(3)
	var aad []byte
	if L.GetTop() >= 4 && L.Get(4) != lua.LNil {
		aad = []byte(L.CheckString(4))
	}
	gcm, _ := cipher.NewGCM(block)
	return gcm, []byte(nonce), []byte(data), aad
}

func cryptoGcmEncrypt(L *lua.LState) int {
	gcm, nonce, data, aad := checkGcm(L, "aesGcmEncrypt")
	L.Push(lua.LString(gcm.Seal(nil, nonce, data, aad)))
	return 1
}

func cryptoGcmDecrypt(L *lua.LState) int {
	gcm, nonce, data, aad := checkGcm(L, "aesGcmDecrypt")
	plain, err := gcm.Open(nil, nonce, data, aad)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("Crypto.aesGcmDecrypt: authentication failed"))
		return 2
	}
	L.Push(lua.LString(plain))
	return 1
}

// cryptoBlockMode builds the ECB and CBC functions: (key, [iv,] data [, padding]).
func cryptoBlockMode(fn string, cbc, encrypt bool) lua.LGFunction {
	return func(L *lua.LState) int {
		block := checkAesKey(L, fn, 1)
		n := 2
		var iv []byte
		if cbc {
			iv = []byte(L.CheckString(2))
			if len(iv) != aes.BlockSize {
				L.RaiseError("Crypto.%s: iv must be %d bytes, got %d", fn, aes.BlockSize, len(iv))
			}
			n = 3
		}
		data := []byte(L.CheckString(n))
		padding := L.OptString(n+1, "pkcs7")
		if padding != "pkcs7" && padding != "none" {
			L.RaiseError("Crypto.%s: padding must be \"pkcs7\" or \"none\", got %q", fn, padding)
		}

		if encrypt && padding == "pkcs7" {
			p := aes.BlockSize - len(data)%aes.BlockSize
			for i := 0; i < p; i++ {
				data = append(data, byte(p))
			}
		}
		if len(data)%aes.BlockSize != 0 || (!encrypt && padding == "pkcs7" && len(data) == 0) {
			L.RaiseError("Crypto.%s: data must be a multiple of %d bytes, got %d", fn, aes.BlockSize, len(data))
		}

		out := make([]byte, len(data))
		switch {
		case cbc && encrypt:
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		case cbc:
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
		default:
			for i := 0; i < len(data); i += aes.BlockSize {
				if encrypt {
					block.Encrypt(out[i:], data[i:])
				} else {
					block.Decrypt(out[i:], data[i:])
				}
			}
		}

		if !encrypt && padding == "pkcs7" {
			p := int(out[len(out)-1])
			if p == 0 || p > aes.BlockSize {
				L.RaiseError("Crypto.%s: bad pkcs7 padding", fn)
			}
			for _, b := range out[len(out)-p:] {
				if int(b) != p {
					L.RaiseError("Crypto.%s: bad pkcs7 padding", fn)
				}
			}
			out = out[:len(out)-p]
		}
		L.Push(lua.LString(out))
		return 1
	}
}

func cryptoRandom(L *lua.LState) int {
	n := L.CheckNumber(1)
	if float64(n) != math.Trunc(float64(n)) || n < 1 || n > 1024 {
		L.RaiseError("Crypto.random: n must be a whole number from 1 to 1024, got %v", n)
	}
	b := make([]byte, int(n))
	if _, err := rand.Read(b); err != nil {
		L.RaiseError("Crypto.random: %v", err)
	}
	L.Push(lua.LString(b))
	return 1
}

func cryptoToHex(L *lua.LState) int {
	L.Push(lua.LString(hex.EncodeToString([]byte(L.CheckString(1)))))
	return 1
}

func cryptoFromHex(L *lua.LState) int {
	b, err := hex.DecodeString(L.CheckString(1))
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("Crypto.fromHex: %v", err)))
		return 2
	}
	L.Push(lua.LString(b))
	return 1
}

// toBase64 is standard with padding; urlSafe is base64url without padding, as in JWT.
func cryptoToBase64(L *lua.LState) int {
	enc := base64.StdEncoding
	if L.ToBool(2) {
		enc = base64.RawURLEncoding
	}
	L.Push(lua.LString(enc.EncodeToString([]byte(L.CheckString(1)))))
	return 1
}

func cryptoFromBase64(L *lua.LState) int {
	text := strings.TrimRight(L.CheckString(1), "=")
	text = strings.NewReplacer("-", "+", "_", "/").Replace(text)
	b, err := base64.RawStdEncoding.DecodeString(text)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("Crypto.fromBase64: %v", err)))
		return 2
	}
	L.Push(lua.LString(b))
	return 1
}

// checkBit converts a number to a 32-bit integer the way BitOp's tobit does: rounded, modulo 2^32.
func checkBit(L *lua.LState, n int) int32 {
	f := math.RoundToEven(float64(L.CheckNumber(n)))
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Mod(f, 4294967296)
	if f < 0 {
		f += 4294967296
	}
	return int32(uint32(f))
}

func pushBit(L *lua.LState, v int32) int {
	L.Push(lua.LNumber(v))
	return 1
}

func bitFold(op func(a, b int32) int32) lua.LGFunction {
	return func(L *lua.LState) int {
		r := checkBit(L, 1)
		for i := 2; i <= L.GetTop(); i++ {
			r = op(r, checkBit(L, i))
		}
		return pushBit(L, r)
	}
}

// A shift count uses its low 5 bits, as in BitOp: lshift(1, 33) == 2.
func bitShift(op func(x int32, n uint) int32) lua.LGFunction {
	return func(L *lua.LState) int {
		return pushBit(L, op(checkBit(L, 1), uint(checkBit(L, 2))&31))
	}
}

func bitToHex(L *lua.LState) int {
	x := uint32(checkBit(L, 1))
	n := 8
	if L.GetTop() >= 2 {
		n = int(checkBit(L, 2))
	}
	format := "%08x"
	if n < 0 {
		n, format = -n, "%08X"
	}
	if n > 8 {
		n = 8
	}
	s := fmt.Sprintf(format, x)
	L.Push(lua.LString(s[8-n:]))
	return 1
}
