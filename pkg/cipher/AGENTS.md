# pkg/cipher: Grenton discovery encryption

This is the discovery protocol (AES-128-CBC, three-step authentication) that Object Manager and
real CLUs expect byte for byte. One wrong byte and vCLU loses the hardware, and nothing in CI uses
real hardware. Changes here are `core`: read the existing tests first and keep them passing; never
change test vectors to make a change pass.
