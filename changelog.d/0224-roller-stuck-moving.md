### Grenton

- **A roller at its end no longer stays "opening"** - `open()` on an open roller or `close()` on a closed one assumes no movement, since the CLU reports none; a movement the CLU never confirms ends after 10 s. Dashboards, Home Assistant and HomeKit show the roller stopped ([#224](https://github.com/Domktorymysli/vclu/issues/224)).
