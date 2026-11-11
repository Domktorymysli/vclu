### Web

- **Disk space on the status page, and a warning before the disk fills** - a Storage card on Stats (where `/status` leads) shows the data directory's file system (size, free, free in %), what vCLU keeps on it (history, metrics, backups of updates, promotes and the panel, plugins, the rest of the data directory), history written in the last 24 h, log output to stdout in the last hour and "at this rate the disk is full in N days" under 90 days. Every panel page shows a yellow banner below 10% or 500 MB free (whichever is larger) and a red one below 3% or 100 MB, naming what takes the most space. Directories are walked at start and every 15 minutes off the Lua loop; free space is read every minute ([#196](https://github.com/Domktorymysli/vclu/issues/196)).

### API

- **`GET /api/storage`** - the same numbers as JSON; `/metrics` (Prometheus) adds `vclu_disk_free_bytes`, `vclu_history_bytes`, `vclu_log_bytes_per_hour`, and dashboards and MCP `vclu_metrics` get them as `vclu:disk_free_bytes`, `vclu:history_bytes`, `vclu:log_bytes_per_hour` every 15 minutes ([#196](https://github.com/Domktorymysli/vclu/issues/196)).

### History

- **History gives way on a nearly full disk** - below the red line (3% or 100 MB free) history removes day files older than 7 days even when `retentionDays` keeps more, and logs it once per episode; today's file and the 7 days before it are never removed by this, and `maxDiskMB` still applies ([#196](https://github.com/Domktorymysli/vclu/issues/196)).
