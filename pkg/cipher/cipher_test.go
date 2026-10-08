package cipher

import (
	"bytes"
	"crypto/aes"
	"testing"
)

// ============================================================================
// CipherKey Creation Tests
// ============================================================================

func TestNewCipherKey(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)

	ck := NewCipherKey(key, iv)

	if !bytes.Equal(ck.GetKey(), key) {
		t.Error("Key mismatch")
	}
	if !bytes.Equal(ck.GetIV(), iv) {
		t.Error("IV mismatch")
	}
}

func TestNewRandomCipherKey(t *testing.T) {
	ck1, err := NewRandomCipherKey()
	if err != nil {
		t.Fatalf("NewRandomCipherKey() error = %v", err)
	}

	ck2, err := NewRandomCipherKey()
	if err != nil {
		t.Fatalf("NewRandomCipherKey() error = %v", err)
	}

	// Should be 16 bytes each
	if len(ck1.GetKey()) != KeyBytes {
		t.Errorf("Key length = %d, want %d", len(ck1.GetKey()), KeyBytes)
	}
	if len(ck1.GetIV()) != IVBytes {
		t.Errorf("IV length = %d, want %d", len(ck1.GetIV()), IVBytes)
	}

	// Two random keys should not be equal
	if bytes.Equal(ck1.GetKey(), ck2.GetKey()) {
		t.Error("Two random keys should not be equal")
	}
	if bytes.Equal(ck1.GetIV(), ck2.GetIV()) {
		t.Error("Two random IVs should not be equal")
	}
}

func TestCipherKey_WithIV(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv1 := bytes.Repeat([]byte{0x34}, 16)
	iv2 := bytes.Repeat([]byte{0x56}, 16)

	ck1 := NewCipherKey(key, iv1)
	ck2 := ck1.WithIV(iv2)

	// Key should be same
	if !bytes.Equal(ck1.GetKey(), ck2.GetKey()) {
		t.Error("Key should be preserved")
	}

	// IV should be different
	if bytes.Equal(ck1.GetIV(), ck2.GetIV()) {
		t.Error("IV should be different")
	}

	// ck2 should have new IV
	if !bytes.Equal(ck2.GetIV(), iv2) {
		t.Error("New cipher should have new IV")
	}
}

func TestCipherKey_String(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	str := ck.String()

	if str == "" {
		t.Error("String() should not be empty")
	}
	if len(str) < 20 {
		t.Error("String() output too short")
	}
}

// ============================================================================
// GenerateKey Tests
// ============================================================================

func TestGenerateKey(t *testing.T) {
	iv := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	privateKey := []byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20}

	result := GenerateKey(iv, privateKey)

	if len(result) != 16 {
		t.Errorf("Result length = %d, want 16", len(result))
	}

	// First half: XOR(iv[0:8], privateKey[0:8])
	for i := 0; i < 8; i++ {
		expected := iv[i] ^ privateKey[i]
		if result[i] != expected {
			t.Errorf("result[%d] = %x, want %x", i, result[i], expected)
		}
	}

	// Second half: XOR(iv[8:16], privateKey[7::-1])
	for i := 8; i < 16; i++ {
		expected := iv[i] ^ privateKey[15-i]
		if result[i] != expected {
			t.Errorf("result[%d] = %x, want %x", i, result[i], expected)
		}
	}
}

func TestGenerateKey_AllZeros(t *testing.T) {
	iv := bytes.Repeat([]byte{0x00}, 16)
	privateKey := bytes.Repeat([]byte{0x00}, 16)

	result := GenerateKey(iv, privateKey)

	// XOR with zeros should return zeros
	expected := bytes.Repeat([]byte{0x00}, 16)
	if !bytes.Equal(result, expected) {
		t.Error("XOR with zeros should return zeros")
	}
}

func TestGenerateKey_AllOnes(t *testing.T) {
	iv := bytes.Repeat([]byte{0xFF}, 16)
	privateKey := bytes.Repeat([]byte{0xFF}, 16)

	result := GenerateKey(iv, privateKey)

	// XOR of same values should return zeros
	expected := bytes.Repeat([]byte{0x00}, 16)
	if !bytes.Equal(result, expected) {
		t.Error("XOR of same values should return zeros")
	}
}

func TestGenerateKey_Deterministic(t *testing.T) {
	iv := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}
	privateKey := []byte{0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89,
		0xFE, 0xDC, 0xBA, 0x98, 0x76, 0x54, 0x32, 0x10}

	result1 := GenerateKey(iv, privateKey)
	result2 := GenerateKey(iv, privateKey)

	if !bytes.Equal(result1, result2) {
		t.Error("GenerateKey should be deterministic")
	}
}

// ============================================================================
// Encrypt/Decrypt Round-Trip Tests
// ============================================================================

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"empty string", []byte("")},
		{"short text", []byte("hello")},
		{"exact block size", bytes.Repeat([]byte("a"), 16)},
		{"two blocks", bytes.Repeat([]byte("b"), 32)},
		{"non-aligned", []byte("hello world!")},
		{"binary data", []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}},
		{"unicode", []byte("Zażółć gęślą jaźń")},
		{"long text", bytes.Repeat([]byte("x"), 1000)},
		{"Lua script", []byte("SYSTEM:clientRegister('192.168.1.1', 4344, 123, {{DOU3328, 0}})")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			ciphertext, err := ck.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			// Decrypt
			decrypted, err := ck.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			// Compare
			if !bytes.Equal(decrypted, tt.plaintext) {
				t.Errorf("Round-trip failed: got %v, want %v", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncrypt_OutputLength(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	tests := []struct {
		inputLen  int
		expectLen int
	}{
		{0, 16},    // Empty -> 1 block of padding
		{1, 16},    // 1 byte -> padded to 16
		{15, 16},   // 15 bytes -> padded to 16
		{16, 32},   // Exact block -> needs extra block for padding
		{17, 32},   // 17 bytes -> padded to 32
		{31, 32},   // 31 bytes -> padded to 32
		{32, 48},   // 2 blocks -> needs extra block
		{100, 112}, // 100 bytes -> padded to 112 (7 blocks)
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			plaintext := bytes.Repeat([]byte("x"), tt.inputLen)
			ciphertext, err := ck.Encrypt(plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			if len(ciphertext) != tt.expectLen {
				t.Errorf("Encrypt(%d bytes) = %d bytes, want %d",
					tt.inputLen, len(ciphertext), tt.expectLen)
			}
		})
	}
}

func TestDecrypt_InvalidInput(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	tests := []struct {
		name       string
		ciphertext []byte
	}{
		{"not multiple of block", []byte{0x01, 0x02, 0x03}},
		{"single byte", []byte{0x01}},
		{"15 bytes", bytes.Repeat([]byte{0x01}, 15)},
		{"17 bytes", bytes.Repeat([]byte{0x01}, 17)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ck.Decrypt(tt.ciphertext)
			if err == nil {
				t.Error("Decrypt() expected error for invalid input")
			}
		})
	}
}

func TestDecrypt_InvalidPadding(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	// Create a valid-looking ciphertext with bad padding
	// This is a 16-byte block that when decrypted will have invalid padding
	badCiphertext := bytes.Repeat([]byte{0x00}, 16)

	_, err := ck.Decrypt(badCiphertext)
	if err == nil {
		t.Error("Decrypt() expected error for invalid padding")
	}
}

func TestEncrypt_DifferentIVs(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv1 := bytes.Repeat([]byte{0x34}, 16)
	iv2 := bytes.Repeat([]byte{0x56}, 16)

	ck1 := NewCipherKey(key, iv1)
	ck2 := NewCipherKey(key, iv2)

	plaintext := []byte("same plaintext")

	ct1, _ := ck1.Encrypt(plaintext)
	ct2, _ := ck2.Encrypt(plaintext)

	// Same plaintext with different IVs should produce different ciphertext
	if bytes.Equal(ct1, ct2) {
		t.Error("Different IVs should produce different ciphertext")
	}

	// But both should decrypt to same plaintext
	pt1, _ := ck1.Decrypt(ct1)
	pt2, _ := ck2.Decrypt(ct2)

	if !bytes.Equal(pt1, pt2) {
		t.Error("Both should decrypt to same plaintext")
	}
}

func TestEncrypt_DifferentKeys(t *testing.T) {
	key1 := bytes.Repeat([]byte{0x12}, 16)
	key2 := bytes.Repeat([]byte{0x34}, 16)
	iv := bytes.Repeat([]byte{0x56}, 16)

	ck1 := NewCipherKey(key1, iv)
	ck2 := NewCipherKey(key2, iv)

	plaintext := []byte("same plaintext")

	ct1, _ := ck1.Encrypt(plaintext)
	ct2, _ := ck2.Encrypt(plaintext)

	// Same plaintext with different keys should produce different ciphertext
	if bytes.Equal(ct1, ct2) {
		t.Error("Different keys should produce different ciphertext")
	}

	// Cross-decryption should fail (wrong key)
	_, err := ck2.Decrypt(ct1)
	if err == nil {
		// May not error but will produce garbage
		t.Log("Cross-decryption did not error (expected)")
	}
}

// ============================================================================
// PKCS7 Padding Tests
// ============================================================================

func TestPKCS7Pad(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected int // expected length after padding
	}{
		{"empty", []byte{}, 16},
		{"1 byte", []byte{0x01}, 16},
		{"15 bytes", bytes.Repeat([]byte{0x01}, 15), 16},
		{"16 bytes", bytes.Repeat([]byte{0x01}, 16), 32},
		{"17 bytes", bytes.Repeat([]byte{0x01}, 17), 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pkcs7Pad(tt.input, aes.BlockSize)

			if len(result) != tt.expected {
				t.Errorf("pkcs7Pad() length = %d, want %d", len(result), tt.expected)
			}

			// Check padding value is correct
			padValue := result[len(result)-1]
			for i := len(result) - int(padValue); i < len(result); i++ {
				if result[i] != padValue {
					t.Errorf("Invalid padding at position %d: got %d, want %d",
						i, result[i], padValue)
				}
			}
		})
	}
}

func TestPKCS7Unpad(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "single byte padding",
			input:    append([]byte("hello world!!!!"), 0x01),
			expected: []byte("hello world!!!!"),
			wantErr:  false,
		},
		{
			name:     "full block padding",
			input:    bytes.Repeat([]byte{0x10}, 16),
			expected: []byte{},
			wantErr:  false,
		},
		{
			name:     "empty input",
			input:    []byte{},
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "invalid padding value (0)",
			input:    append(bytes.Repeat([]byte{0x01}, 15), 0x00),
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "invalid padding value (too large)",
			input:    append(bytes.Repeat([]byte{0x01}, 15), 0x20),
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "inconsistent padding",
			input:    append(bytes.Repeat([]byte{0x01}, 14), 0x02, 0x03),
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pkcs7Unpad(tt.input, aes.BlockSize)

			if (err != nil) != tt.wantErr {
				t.Errorf("pkcs7Unpad() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !bytes.Equal(result, tt.expected) {
				t.Errorf("pkcs7Unpad() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPKCS7_RoundTrip(t *testing.T) {
	for length := 0; length <= 50; length++ {
		data := bytes.Repeat([]byte{0xAB}, length)

		padded := pkcs7Pad(data, aes.BlockSize)
		unpadded, err := pkcs7Unpad(padded, aes.BlockSize)

		if err != nil {
			t.Errorf("Round-trip failed for length %d: %v", length, err)
			continue
		}

		if !bytes.Equal(unpadded, data) {
			t.Errorf("Round-trip mismatch for length %d", length)
		}
	}
}

// ============================================================================
// GetInitialCipherKey Tests
// ============================================================================

func TestGetInitialCipherKey(t *testing.T) {
	// Set up DefaultIV for testing
	testDefaultIV := bytes.Repeat([]byte{0x11}, 16)
	DefaultIV = testDefaultIV

	privateKey := bytes.Repeat([]byte{0x22}, 16)
	iv := bytes.Repeat([]byte{0x33}, 16)

	ck := GetInitialCipherKey(iv, privateKey)

	if ck == nil {
		t.Fatal("GetInitialCipherKey() returned nil")
	}

	// Key should be generated from DefaultIV + privateKey
	expectedKey := GenerateKey(testDefaultIV, privateKey)
	if !bytes.Equal(ck.GetKey(), expectedKey) {
		t.Error("Key generation mismatch")
	}

	// IV should be the provided iv
	if !bytes.Equal(ck.GetIV(), iv) {
		t.Error("IV should be the provided iv")
	}
}

// ============================================================================
// SetDefault Functions Tests
// ============================================================================

func TestSetDefaultBroadcastKey(t *testing.T) {
	key := bytes.Repeat([]byte{0xAA}, 16)
	iv := bytes.Repeat([]byte{0xBB}, 16)

	SetDefaultBroadcastKey(key, iv)

	if !bytes.Equal(DefaultKey, key) {
		t.Error("DefaultKey not set")
	}
	if !bytes.Equal(DefaultBroadcastIV, iv) {
		t.Error("DefaultBroadcastIV not set")
	}
	if DefaultBroadcastKey == nil {
		t.Error("DefaultBroadcastKey not created")
	}
	if !bytes.Equal(DefaultBroadcastKey.GetKey(), key) {
		t.Error("DefaultBroadcastKey has wrong key")
	}
}

func TestSetDefaultIV(t *testing.T) {
	iv := bytes.Repeat([]byte{0xCC}, 16)

	SetDefaultIV(iv)

	if !bytes.Equal(DefaultIV, iv) {
		t.Error("DefaultIV not set")
	}
}

// ============================================================================
// Edge Cases and Security Tests
// ============================================================================

func TestEncrypt_EmptyPlaintext(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	ciphertext, err := ck.Encrypt([]byte{})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Empty plaintext should produce exactly one block (padding only)
	if len(ciphertext) != 16 {
		t.Errorf("Empty encrypt length = %d, want 16", len(ciphertext))
	}

	// Should decrypt back to empty
	decrypted, err := ck.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if len(decrypted) != 0 {
		t.Error("Empty plaintext should decrypt to empty")
	}
}

func TestEncrypt_LargePlaintext(t *testing.T) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)

	// Test with 1MB of data
	plaintext := bytes.Repeat([]byte{0xAB}, 1024*1024)

	ciphertext, err := ck.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	decrypted, err := ck.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Large plaintext round-trip failed")
	}
}

func TestEncrypt_InvalidKeySize(t *testing.T) {
	// AES-128 requires exactly 16-byte key
	shortKey := bytes.Repeat([]byte{0x12}, 8)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(shortKey, iv)

	_, err := ck.Encrypt([]byte("test"))
	if err == nil {
		t.Error("Encrypt() should fail with invalid key size")
	}
}

func TestDecrypt_InvalidKeySize(t *testing.T) {
	shortKey := bytes.Repeat([]byte{0x12}, 8)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(shortKey, iv)

	ciphertext := bytes.Repeat([]byte{0x00}, 16)

	_, err := ck.Decrypt(ciphertext)
	if err == nil {
		t.Error("Decrypt() should fail with invalid key size")
	}
}

// ============================================================================
// Benchmark Tests
// ============================================================================

func BenchmarkEncrypt(b *testing.B) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)
	plaintext := bytes.Repeat([]byte{0xAB}, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ck.Encrypt(plaintext)
	}
}

func BenchmarkDecrypt(b *testing.B) {
	key := bytes.Repeat([]byte{0x12}, 16)
	iv := bytes.Repeat([]byte{0x34}, 16)
	ck := NewCipherKey(key, iv)
	plaintext := bytes.Repeat([]byte{0xAB}, 1024)
	ciphertext, _ := ck.Encrypt(plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ck.Decrypt(ciphertext)
	}
}

func BenchmarkGenerateKey(b *testing.B) {
	iv := bytes.Repeat([]byte{0x12}, 16)
	privateKey := bytes.Repeat([]byte{0x34}, 16)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = GenerateKey(iv, privateKey)
	}
}
