package lua

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// Contract of the Crypto and bit tables (#257): RFC/NIST vectors, errors, BitOp results.

func cryptoEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	return engine
}

// rep builds a Lua expression for n copies of a byte.
func rep(b string, n int) string {
	return `Crypto.fromHex(("` + b + `"):rep(` + strconv.Itoa(n) + `))`
}

func TestCryptoDigestVectors(t *testing.T) {
	engine := cryptoEngine(t)
	for expr, want := range map[string]string{
		// FIPS 180-2 examples
		`Crypto.sha256("abc")`: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		`Crypto.sha256("")`:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		`Crypto.sha256("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")`: "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
		`Crypto.sha1("abc")`: "a9993e364706816aba3e25717850c26c9cd0d89d",
		`Crypto.sha1("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")`: "84983e441c3bd26ebaae4aa1f95129e5e54670f1",
		// RFC 1321
		`Crypto.md5("")`:               "d41d8cd98f00b204e9800998ecf8427e",
		`Crypto.md5("abc")`:            "900150983cd24fb0d6963f7d28e17f72",
		`Crypto.md5("message digest")`: "f96b697d7cb7938d525a2f31aaf161d0",
		// RFC 4231 test cases 1, 2, 3, 6
		`Crypto.hmac("sha256", ` + rep("0b", 20) + `, "Hi There")`:                                                "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
		`Crypto.hmac("sha256", "Jefe", "what do ya want for nothing?")`:                                           "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
		`Crypto.hmac("sha256", ` + rep("aa", 20) + `, ` + rep("dd", 50) + `)`:                                     "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe",
		`Crypto.hmac("sha256", ` + rep("aa", 131) + `, "Test Using Larger Than Block-Size Key - Hash Key First")`: "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54",
		// RFC 2202
		`Crypto.hmac("sha1", ` + rep("0b", 20) + `, "Hi There")`:      "b617318655057264e28bc0b6fb378c8ef146be00",
		`Crypto.hmac("sha1", "Jefe", "what do ya want for nothing?")`: "effcdf6ae5eb2fa2d27416d5f184df9c259a7c79",
		`Crypto.hmac("md5", ` + rep("0b", 16) + `, "Hi There")`:       "9294727a3638bb1c13f48ef8158bfc9d",
		`Crypto.hmac("md5", "Jefe", "what do ya want for nothing?")`:  "750c783e6ab0b503eaa86e310a5db738",
	} {
		mustLua(t, engine, `Crypto.toHex(`+expr+`)`, want)
	}
}

// NIST SP 800-38D vectors as published with GCM (McGrew & Viega, test cases 1, 2, 4, 16).
func TestCryptoAesGcmVectors(t *testing.T) {
	engine := cryptoEngine(t)
	const (
		p   = "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39"
		aad = "feedfacedeadbeeffeedfacedeadbeefabaddad2"
		iv  = "cafebabefacedbaddecaf888"
	)
	for _, c := range []struct{ key, iv, plain, aad, want string }{
		{"00000000000000000000000000000000", "000000000000000000000000", "", "", "58e2fccefa7e3061367f1d57a4e7455a"},
		{"00000000000000000000000000000000", "000000000000000000000000", "00000000000000000000000000000000", "",
			"0388dace60b6a392f328c2b971b2fe78" + "ab6e47d42cec13bdf53a67b21257bddf"},
		{"feffe9928665731c6d6a8f9467308308", iv, p, aad,
			"42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091" + "5bc94fbc3221a5db94fae95ae7121a47"},
		{"feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", iv, p, aad,
			"522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662" + "76fc6ece0f4e1768cddf8853bb2d551b"},
	} {
		args := `Crypto.fromHex("` + c.key + `"), Crypto.fromHex("` + c.iv + `"), Crypto.fromHex("` + c.plain + `")`
		if c.aad != "" {
			args += `, Crypto.fromHex("` + c.aad + `")`
		}
		mustLua(t, engine, `Crypto.toHex(Crypto.aesGcmEncrypt(`+args+`))`, c.want)

		dec := `Crypto.fromHex("` + c.key + `"), Crypto.fromHex("` + c.iv + `"), Crypto.fromHex("` + c.want + `")`
		if c.aad != "" {
			dec += `, Crypto.fromHex("` + c.aad + `")`
		}
		mustLua(t, engine, `Crypto.toHex(Crypto.aesGcmDecrypt(`+dec+`))`, c.plain)
	}
}

func TestCryptoAesGcmRejectsTampering(t *testing.T) {
	engine := cryptoEngine(t)
	if _, err := engine.Execute(`
		__k = Crypto.fromHex("feffe9928665731c6d6a8f9467308308")
		__n = Crypto.fromHex("cafebabefacedbaddecaf888")
		__c = Crypto.aesGcmEncrypt(__k, __n, "secret payload", "header")
		function __flip(s, i)
			return s:sub(1, i - 1) .. string.char((s:byte(i) + 1) % 256) .. s:sub(i + 1)
		end
		function __try(c, aad)
			local p, err = Crypto.aesGcmDecrypt(__k, __n, c, aad)
			return tostring(p) .. "|" .. tostring(err)
		end
	`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `__try(__c, "header")`, "secret payload|nil")
	for _, expr := range []string{
		`__try(__flip(__c, 1), "header")`,    // ciphertext byte
		`__try(__flip(__c, #__c), "header")`, // tag byte
		`__try(__c, "headeR")`,
		`__try(__c)`,
		`__try(__c:sub(1, -2), "header")`,
	} {
		mustLua(t, engine, expr, "nil|Crypto.aesGcmDecrypt: authentication failed")
	}
}

func TestCryptoAesEcbCbc(t *testing.T) {
	engine := cryptoEngine(t)
	// FIPS-197 C.1 and SP 800-38A F.2.1
	mustLua(t, engine, `Crypto.toHex(Crypto.aesEcbEncrypt(Crypto.fromHex("000102030405060708090a0b0c0d0e0f"), Crypto.fromHex("00112233445566778899aabbccddeeff"), "none"))`,
		"69c4e0d86a7b0430d8cdb78070b4c55a")
	mustLua(t, engine, `Crypto.toHex(Crypto.aesCbcEncrypt(Crypto.fromHex("2b7e151628aed2a6abf7158809cf4f3c"), Crypto.fromHex("000102030405060708090a0b0c0d0e0f"), Crypto.fromHex("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51"), "none"))`,
		"7649abac8119b246cee98e9b12e9197d5086cb9b507219ee95db113a917678b2")

	if _, err := engine.Execute(`
		__k16, __k24, __k32 = ("k"):rep(16), ("k"):rep(24), ("k"):rep(32)
		__iv = ("i"):rep(16)
		function __round(key, data, padding)
			local e = Crypto.aesEcbDecrypt(key, Crypto.aesEcbEncrypt(key, data, padding), padding)
			local c = Crypto.aesCbcDecrypt(key, __iv, Crypto.aesCbcEncrypt(key, __iv, data, padding), padding)
			return tostring(e == data and c == data)
		end
	`); err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{
		`__round(__k16, "")`,
		`__round(__k16, "x")`,
		`__round(__k24, ("\0\255"):rep(8))`,
		`__round(__k32, ("a"):rep(17), "pkcs7")`,
		`__round(__k16, ("\0\128"):rep(16), "none")`,
	} {
		mustLua(t, engine, expr, "true")
	}
	// pkcs7 adds a whole block to a full block
	mustLua(t, engine, `#Crypto.aesEcbEncrypt(__k16, ("a"):rep(16))`, "32")

	// decrypting with the wrong key gives bad padding: an error, never garbage
	for _, code := range []string{
		`Crypto.aesEcbDecrypt(("z"):rep(16), Crypto.aesEcbEncrypt(__k16, "hello"))`,
		`Crypto.aesCbcDecrypt(__k16, __iv, ("\0"):rep(16))`,
		`Crypto.aesEcbDecrypt(__k16, "")`,
		`Crypto.aesEcbEncrypt(__k16, "short", "none")`,
		`Crypto.aesEcbEncrypt(__k16, "x", "zeros")`,
	} {
		mustLua(t, engine, `tostring(pcall(function() return `+code+` end))`, "false")
	}
}

func TestCryptoErrorsNameTheExpectedLength(t *testing.T) {
	engine := cryptoEngine(t)
	for code, want := range map[string]string{
		`Crypto.aesGcmEncrypt("short", ("n"):rep(12), "x")`:       "key must be 16, 24 or 32 bytes, got 5",
		`Crypto.aesGcmEncrypt(("k"):rep(16), ("n"):rep(8), "x")`:  "nonce must be 12 bytes, got 8",
		`Crypto.aesCbcEncrypt(("k"):rep(16), ("i"):rep(15), "x")`: "iv must be 16 bytes, got 15",
		`Crypto.aesEcbDecrypt(("k"):rep(32), "x", "none")`:        "data must be a multiple of 16 bytes, got 1",
		`Crypto.hmac("sha512", "k", "x")`:                         `alg must be "sha256", "sha1" or "md5", got "sha512"`,
		`Crypto.aesEcbEncrypt(("k"):rep(16), "x", "zero")`:        `padding must be "pkcs7" or "none", got "zero"`,
		`Crypto.random(0)`: "n must be a whole number from 1 to 1024, got 0",
	} {
		// the error points at the caller's line (line 3 of the chunk)
		got, err := engine.Execute("local ok, e = pcall(function()\n\n" + code + "\nend)\nreturn e")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, want) || !strings.Contains(got, ":3:") {
			t.Errorf("%s: error %q, want %q at line 3", code, got, want)
		}
	}
}

func TestCryptoHexBase64RoundTrip(t *testing.T) {
	engine := cryptoEngine(t)
	if _, err := engine.Execute(`
		local t = {}
		for i = 0, 255 do t[#t + 1] = string.char(i) end
		__all = table.concat(t) .. "\0\0\255"
	`); err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{
		`Crypto.fromHex(Crypto.toHex(__all)) == __all`,
		`Crypto.fromHex(Crypto.toHex(__all):upper()) == __all`,
		`Crypto.fromBase64(Crypto.toBase64(__all)) == __all`,
		`Crypto.fromBase64(Crypto.toBase64(__all, true)) == __all`,
		`Crypto.fromBase64(Crypto.toBase64("\0")) == "\0"`,
	} {
		mustLua(t, engine, `tostring(`+expr+`)`, "true")
	}
	mustLua(t, engine, `Crypto.toBase64("\251\255")`, "+/8=")
	mustLua(t, engine, `Crypto.toBase64("\251\255", true)`, "-_8")
	for _, in := range []string{"+/8=", "+/8", "-_8=", "-_8"} {
		mustLua(t, engine, `Crypto.toHex(Crypto.fromBase64("`+in+`"))`, "fbff")
	}
	mustLua(t, engine, `select("#", Crypto.fromHex("abc")) .. tostring(Crypto.fromHex("zz"))`, "2nil")
	mustLua(t, engine, `tostring(Crypto.fromBase64("a!b="))`, "nil")
	mustLua(t, engine, `require("PluginHelpers").basicAuth("user", "pass")`, "Basic dXNlcjpwYXNz")
}

func TestCryptoRandom(t *testing.T) {
	engine := cryptoEngine(t)
	mustLua(t, engine, `#Crypto.random(1) .. "," .. #Crypto.random(1024)`, "1,1024")
	mustLua(t, engine, `tostring(Crypto.random(32) ~= Crypto.random(32))`, "true")
	// 256 bytes from crypto/rand: far more than a handful of distinct values
	mustLua(t, engine, `(function() local s, n = {}, 0; for c in Crypto.random(256):gmatch(".") do if not s[c] then s[c] = true; n = n + 1 end end; return tostring(n > 100) end)()`, "true")
	for _, n := range []string{"0", "1025", "-1", "1.5"} {
		mustLua(t, engine, `tostring(pcall(Crypto.random, `+n+`))`, "false")
	}
}

// Results as LuaJIT 2.1's BitOp gives them.
func TestBitMatchesBitOp(t *testing.T) {
	engine := cryptoEngine(t)
	for expr, want := range map[string]string{
		`bit.band(-1, 0xff)`:               "255",
		`bit.band(0xffffffff)`:             "-1",
		`bit.band(0xff, 0xf0, 0x30)`:       "48",
		`bit.bor(2^32 + 1, 0)`:             "1",
		`bit.bor(1, 2, 4, 8)`:              "15",
		`bit.bxor(0x12345678, 0xffffffff)`: "-305419897",
		`bit.bnot(0)`:                      "-1",
		`bit.bnot(-1)`:                     "0",
		`bit.bnot(0x7fffffff)`:             "-2147483648",
		`bit.lshift(1, 31)`:                "-2147483648",
		`bit.lshift(1, 32)`:                "1",
		`bit.lshift(1, 33)`:                "2",
		`bit.lshift(0xff, -8)`:             "-16777216",
		`bit.rshift(-1, 28)`:               "15",
		`bit.rshift(-1, 0)`:                "-1",
		`bit.rshift(256, 36)`:              "16",
		`bit.arshift(-256, 4)`:             "-16",
		`bit.arshift(-1, 31)`:              "-1",
		`bit.arshift(0x80000000, 31)`:      "-1",
		`bit.tohex(-1)`:                    "ffffffff",
		`bit.tohex(255, 2)`:                "ff",
		`bit.tohex(255, -4)`:               "00FF",
		`bit.tohex(0x1234abcd, 12)`:        "1234abcd",
		`bit.tohex(-2)`:                    "fffffffe",
	} {
		mustLua(t, engine, `tostring(`+expr+`)`, want)
	}
}

// A Tuya OpenAPI request signature built in Lua, compared with one computed in Go.
func TestCryptoTuyaStyleSignature(t *testing.T) {
	engine := cryptoEngine(t)
	got, err := engine.Execute(`
		local clientId, secret, token = "abcd1234", "s3cr3t", "tok-1"
		local t = "1700000000000"
		local body = '{"commands":[{"code":"switch_1","value":true}]}'
		local stringToSign = "POST\n" .. Crypto.toHex(Crypto.sha256(body)) .. "\n\n/v1.0/devices/d1/commands"
		return Crypto.toHex(Crypto.hmac("sha256", secret, clientId .. token .. t .. stringToSign)):upper()
	`)
	if err != nil {
		t.Fatal(err)
	}
	body := sha256.Sum256([]byte(`{"commands":[{"code":"switch_1","value":true}]}`))
	m := hmac.New(sha256.New, []byte("s3cr3t"))
	m.Write([]byte("abcd1234tok-11700000000000POST\n" + hex.EncodeToString(body[:]) + "\n\n/v1.0/devices/d1/commands"))
	if want := strings.ToUpper(hex.EncodeToString(m.Sum(nil))); got != want {
		t.Errorf("signature %s, want %s", got, want)
	}
}

// Plugins on API 1 and 2 reach every function without a permission; a change stays in the plugin.
func TestPluginCryptoAndBitWithoutPermission(t *testing.T) {
	engine := sandboxEngine(t, "@t/v1", "{api = 1}")
	if _, err := engine.Execute(`PluginSandbox:create("@t/v2", {api = 2})`); err != nil {
		t.Fatal(err)
	}
	const everything = `
		local k, n = ("k"):rep(16), ("n"):rep(12)
		local out = {
			Crypto.toHex(Crypto.sha256("abc")):sub(1, 8),
			Crypto.toHex(Crypto.sha1("abc")):sub(1, 8),
			Crypto.toHex(Crypto.md5("abc")):sub(1, 8),
			#Crypto.hmac("sha256", "k", "x"),
			Crypto.aesGcmDecrypt(k, n, Crypto.aesGcmEncrypt(k, n, "g", "a"), "a"),
			Crypto.aesEcbDecrypt(k, Crypto.aesEcbEncrypt(k, "e")),
			Crypto.aesCbcDecrypt(k, k, Crypto.aesCbcEncrypt(k, k, "c")),
			#Crypto.random(4),
			Crypto.fromHex("6869"), Crypto.fromBase64(Crypto.toBase64("b64", true)),
			bit.band(6, 3), bit.bor(1, 2), bit.bxor(3, 1), bit.bnot(0),
			bit.lshift(1, 4), bit.rshift(16, 4), bit.arshift(-16, 2), bit.tohex(255, 2),
		}
		for i = 1, #out do out[i] = tostring(out[i]) end
		return table.concat(out, ",")
	`
	const want = "ba7816bf,a9993e36,90015098,32,g,e,c,4,hi,b64,2,3,2,-1,16,1,-4,ff"
	for _, id := range []string{"@t/v1", "@t/v2"} {
		r, e := runPlugin(t, engine, id, everything)
		if e != "nil" || r != want {
			t.Errorf("%s: r=%s e=%s, want %s", id, r, e, want)
		}
	}

	r, e := runPlugin(t, engine, "@t/v2", `
		Crypto.sha256 = function() return "pwned" end
		bit.bxor = function() return 0 end
		Crypto.hmac = nil
		return Crypto.sha256("x") .. bit.bxor(1, 2)
	`)
	if e != "nil" || r != "pwned0" {
		t.Fatalf("own copies must be writable: r=%s e=%s", r, e)
	}
	// modules and the other plugin keep the real functions
	mustLua(t, engine, `Crypto.toHex(Crypto.sha256("abc")):sub(1, 8) .. bit.bxor(1, 2) .. type(Crypto.hmac)`, "ba7816bf3function")
	r, _ = runPlugin(t, engine, "@t/v1", `return Crypto.toHex(Crypto.sha256("abc")):sub(1, 8) .. bit.bxor(1, 2) .. type(Crypto.hmac)`)
	if r != "ba7816bf3function" {
		t.Errorf("another plugin sees the change: %s", r)
	}
	// a plugin created after the change gets the real functions too
	if _, err := engine.Execute(`PluginSandbox:create("@t/v3", {api = 2})`); err != nil {
		t.Fatal(err)
	}
	r, _ = runPlugin(t, engine, "@t/v3", `return bit.bxor(1, 2) .. type(Crypto.hmac)`)
	if r != "3function" {
		t.Errorf("a new plugin sees the change: %s", r)
	}
}
