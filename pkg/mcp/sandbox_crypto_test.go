package mcp

import "testing"

// A draft sees Crypto and bit as a module in production does (#257).
func TestSandboxDraftHasCryptoAndBit(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/sign.lua", `
		local sig = Crypto.toHex(Crypto.hmac("sha256", "Jefe", "what do ya want for nothing?"))
		if sig ~= "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" then error("hmac " .. sig) end
		if bit.bxor(5, 3) ~= 6 then error("bit") end
	`)
	result, err := se.RunDraft([]string{"modules/sign.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft: %v", err)
	}
	if !result.Success {
		t.Fatalf("Crypto/bit missing in the sandbox: errors=%v", result.Errors)
	}
}
