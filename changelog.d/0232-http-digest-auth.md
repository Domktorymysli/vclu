### Lua runtime

- **HTTP Digest authentication** - `HTTP:new("door"):setDigestAuth("admin", "secret"):put(url, xml)` answers a Digest challenge (MD5, SHA-256, `-sess`, `qop=auth`) for any method and sends the body again; plugins pass `digest = { user = ..., password = ... }` to `httpRequest`. Devices that refuse Basic, such as Hikvision intercoms (ISAPI), work without a separate server. A server that does not ask for Digest gets no credentials. Camera snapshots with `auth = "digest"` use the same code ([#232](https://github.com/Domktorymysli/vclu/issues/232)).
