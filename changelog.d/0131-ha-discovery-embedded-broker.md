### Home Assistant

- **Discovery arrives at startup with the embedded broker** - with the embedded broker and no `mqttClient` section, the Home Assistant integration is set up once the client has connected, so the discovery configs and `vclu/status` reach the broker instead of timing out; before, they appeared only after "publish discovery" ([#131](https://github.com/Domktorymysli/vclu/issues/131)).
