# pkg/homekit: HomeKit bridge

## The Home app caches names and layout
It re-reads accessories only when the bridge's configuration number changes, and a rename alone
does not change the HAP configuration hash. `bumpOnRename` in `server.go` raises the number when
names change; keep new name sources going through it.

## Accessory ids are forever
Rooms and automations in the Home app hang on the accessory id. Ids are kept per path in
`aid_store.go`; never reuse the id of a removed object and never derive ids from map order.

## Names arrive after start
Some names (Satel zones) come from the device seconds after vCLU starts, when the bridge is already
built. A name given in `expose()` wins over a name from a device; the bridge must pick up later
renames.

## A colour is two writes
The Home app sends Hue and Saturation separately; `addColor` in `color.go` waits
`colorWriteDelay` and sends one `setColor`, or the CLU gets two colours in a row. Tests replace
`afterColorWrite` instead of sleeping (`color_test.go`). A new characteristic on an existing
accessory changes the configuration number: keep it to the objects that need it.

## Satel is read-only here on purpose
A write from the Home app would change vCLU's state without sending anything to the alarm panel.
