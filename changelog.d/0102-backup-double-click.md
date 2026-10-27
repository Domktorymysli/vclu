### Web

- **One click on "Create backup" makes one backup** - the button had two click handlers, so every manual backup was written twice (`backup_..._15-03.zip` and `backup_..._15-03-23.zip`) and duplicates pushed older backups out under `maxBackups` ([#102](https://github.com/Domktorymysli/vclu/issues/102)).
