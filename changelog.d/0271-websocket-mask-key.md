### Security

- **gorilla/websocket with the fix for GO-2026-6278** - the cloud client's WebSocket frames get
  their mask key from a secure random generator ([#271](https://github.com/Domktorymysli/vclu/issues/271)).
