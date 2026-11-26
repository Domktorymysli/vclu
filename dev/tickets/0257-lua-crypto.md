# #257: Crypto and bit operations for modules and plugins: hashes, HMAC, AES, random bytes

- **Type:** feature
- **Area:** lua-runtime, plugins, sandbox
- **Size:** M
- **Epic:** —
- **Blocked by:** #205 (built on the PR #206 branch, which changes the sandbox environment)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module or a plugin signs and encrypts requests for cloud and local device APIs in Lua, e.g.
`Crypto.toHex(Crypto.hmac("sha256", secret, payload)):upper()`, with no permission needed.

## Context
Many integrations cannot be written as plugins today because the Lua runtime has no
cryptography. A community member wants to write a Tuya plugin: its cloud API signs every request
with HMAC-SHA256 and a SHA-256 of the body, the newer API encrypts bodies with AES-GCM, and the
local protocol (TCP 6668) uses AES-ECB, AES-GCM and MD5. Other cloud APIs (eWeLink, Xiaomi,
request signing in the style of AWS) need the same pieces. gopher-lua is Lua 5.1, so there are
no bit operators either, and binary protocols need XOR and shifts.

Go already has what is needed (`crypto/*` is used in `pkg/cipher`, `pkg/satel`, `pkg/httpdigest`),
but nothing reaches Lua. `PluginHelpers.base64Encode` exists in pure Lua; there is no decoder.

These are pure functions: no network, no files, no state, so no permission. They go to modules
and to the plugin sandbox on both API versions (see `dev/plans` / #205 for API 1 and 2). On
PR #206 the sandbox gets its own copies of the standard libraries; `Crypto` and `bit` are added
the same way, as read-only tables a plugin cannot change for others.

Lua strings are byte strings, so binary input and output are plain strings; hex and base64 are
separate helpers.

## API
`Crypto` (global table):
- `Crypto.sha256(data)`, `Crypto.sha1(data)`, `Crypto.md5(data)` → raw digest
- `Crypto.hmac(alg, key, data)` → raw MAC; `alg` is `"sha256"`, `"sha1"` or `"md5"`
- `Crypto.aesGcmEncrypt(key, nonce, plaintext [, aad])` → ciphertext with the 16-byte tag at the end
- `Crypto.aesGcmDecrypt(key, nonce, ciphertext [, aad])` → plaintext, or `nil, err` on a bad tag
- `Crypto.aesEcbEncrypt(key, data [, padding])`, `Crypto.aesEcbDecrypt(key, data [, padding])`;
  `padding` is `"pkcs7"` (default) or `"none"`
- `Crypto.aesCbcEncrypt(key, iv, data [, padding])`, `Crypto.aesCbcDecrypt(...)`, same padding
- `Crypto.random(n)` → `n` bytes from `crypto/rand` (1 ≤ n ≤ 1024)
- `Crypto.toHex(bytes)`, `Crypto.fromHex(hex)`, `Crypto.toBase64(bytes [, urlSafe])`,
  `Crypto.fromBase64(text)` (accepts standard and URL-safe, with or without padding)

`bit` (global table, LuaJIT/BitOp names, 32-bit like BitOp):
`band`, `bor`, `bxor`, `bnot`, `lshift`, `rshift`, `arshift`, `tohex`.

Keys of 16, 24 or 32 bytes; a wrong key, nonce or IV length, an unknown `alg` or bad padding raises
an error with the expected length, at the caller's line (see the `error()` level fix in #77).

## Files
- `pkg/lua/crypto.go` (new): Go functions and the `Crypto`/`bit` tables
- `pkg/lua/lua.go`: register them for modules
- `pkg/lua/runtime/core/PluginSandbox.lua`: expose them in the plugin environment
- `pkg/lua/runtime/core/PluginHelpers.lua`: `base64Encode` uses `Crypto.toBase64`
- `docs` repo: a Lua reference page for `Crypto` and `bit`; a line in the plugin permissions page
  that they need no permission
- vclu-plugins `DEVELOPMENT.md`: the same line

## Acceptance criteria
- [ ] SHA-256, SHA-1, MD5 and HMAC match the RFC/NIST test vectors (RFC 4231 for HMAC-SHA256).
- [ ] AES-GCM matches NIST SP 800-38D vectors; a changed byte of the ciphertext, tag or `aad` gives `nil, err`, never garbage.
- [ ] AES-ECB and AES-CBC round-trip with `pkcs7` and `none`; bad padding on decrypt is an error.
- [ ] Hex and base64 round-trip arbitrary bytes, including `\0` and bytes ≥ 0x80.
- [ ] `bit` results match LuaJIT BitOp for negative numbers and shifts ≥ 32.
- [ ] `Crypto.random` returns `n` distinct-looking bytes and rejects `n` outside 1..1024.
- [ ] A plugin on API 1 and on API 2 can call every function without a permission; a plugin cannot replace `Crypto.sha256` or `bit.bxor` for modules or other plugins (sandbox contract test).
- [ ] A Tuya-style signature example in the tests: HMAC-SHA256 of a string built from client id, token, timestamp and body SHA-256, upper-case hex.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0257-lua-crypto.md`.

## Contract impact
Lua API: two new global tables, `Crypto` and `bit`; nothing existing changes. Plugin sandbox
contract: two more tables available without a permission, covered by the escape tests. No
decision record needed.
