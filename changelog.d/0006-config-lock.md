### Web

- **Two settings saved at the same time no longer overwrite each other** - every save to `.vclu.json` (web pages, wizard, MCP bots, plugins, access profiles, pairing) now reads and writes the file under one lock, so a setting saved in one tab no longer disappears when another section is saved a moment later ([#6](https://github.com/Domktorymysli/vclu/issues/6)).
