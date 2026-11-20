### Home Assistant

- **Only the `device_class` Home Assistant knows for the component** - `expose()`'s `device_class` reaches Home Assistant's discovery for a switch only as `outlet` or `switch`, for a cover only as one of HA's cover classes (`awning`, `blind`, `curtain`, `damper`, `door`, `garage`, `gate`, `shade`, `shutter`, `window`), and never for a light; HA refused the whole entity for another one. Sensors and other components get any class as before ([#236](https://github.com/Domktorymysli/vclu/pull/236)).

### Cloud

- **The cloud knows what a device is** - the device list sent to vclu cloud carries `expose()`'s `device_class` as `class` (`gate`, `garage`, `shutter`, `awning`, ...), so an assistant tells a gate or a garage door exposed as a cover or a pulse switch from a blind ([#236](https://github.com/Domktorymysli/vclu/pull/236)).
