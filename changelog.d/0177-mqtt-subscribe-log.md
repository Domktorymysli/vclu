### Fixes

- **No false MQTT warnings at start** - modules load before the MQTT client connects, so their
  subscriptions waited and were made on connect, but each logged `subscribe ... failed: client
  MQTT not found` and the start said `0 subscriptions restored`. Messages always arrived; the
  warnings are gone ([#177](https://github.com/Domktorymysli/vclu/issues/177)).
