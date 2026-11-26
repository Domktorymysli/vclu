### Grenton

- **Remote sync no longer leaves orphan sessions on a CLU** - when a subscription's renewal
  failed to send, vclu dropped it from its own table but left the session registered on the CLU
  until its TTL expired; the object then kept getting pushes under a session vclu no longer knew.
  A failed renewal now sends `clientDestroy` for that session, freeing it on the CLU before the
  fallback re-subscribe registers the object under a fresh session. Shutdown's `destroyAll` also
  spaces its `clientDestroy` packets through a timer chain (the same way renewals are spaced), so
  a CLU no longer drops a back-to-back burst and keeps those sessions alive until their TTL
  ([#276](https://github.com/Domktorymysli/vclu/issues/276)).
- **Default sync batch size raised from 4 to 8** - each `clientRegister` now carries up to 8
  entries, so a CLU holds fewer sessions for the same number of synced objects. Existing saved
  configurations keep their value; the range stays 2-28
  ([#276](https://github.com/Domktorymysli/vclu/issues/276)).
