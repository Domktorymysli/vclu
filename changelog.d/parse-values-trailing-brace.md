### Grenton

- **The first value of the last object in a subscription is a number again** - the CLU's answer to `clientRegister` ends with a newline, so the closing brace stayed on the last value (`"20.50}"`, a string) until the first renewal, up to half the TTL later. Whitespace around the answer is trimmed before the braces are removed.
