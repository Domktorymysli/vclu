### Grenton

- **A ROLLER_SHUTTER on the first firmware (fv01) moves from vclu, Home Assistant and HomeKit** - the OM import gave every class 24 roller a position, which fv01 does not have, so `open()`/`close()` sent `SetPosition` and Home Assistant showed a position slider. A roller whose om.lua sets feature 1 (MaxTime, only on fv01) now moves with MoveUp/MoveDown and has no position or slats. Import om.lua again (or regenerate the proxies) to apply it ([#186](https://github.com/Domktorymysli/vclu/issues/186)).
