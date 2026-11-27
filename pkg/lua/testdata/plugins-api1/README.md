Three official plugins as published for vclu 1.12 (vclu-plugins a013bc0), on plugin API 1:
`TestAPI1PluginsOfVclu112` checks that a newer vclu still runs what people have installed.
Each covers a part of API 1 that API 2 took away: modbus-gateway uses `Modbus` without declaring
it, tasmota-switch the `HTTP` global, example `httpGet`. Never update these files; remove the
directory with plugin API 1.
