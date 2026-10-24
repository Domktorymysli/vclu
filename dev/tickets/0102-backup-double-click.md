# #0102: One click on "Create backup" makes one backup

- **Type:** bug
- **Area:** web
- **Size:** XS
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Pressing "Create backup" on the Backup page sends one `POST /api/backup/create` and leaves one ZIP.

## Context
The button has two click handlers: `onclick="createBackup()"` in `web/templates/backup.html` and
the listener `BackupManager.init` adds in `web/static/js/backup.js`. Both call
`backupManager.create()` in the same click (disabling the button does not stop the second
handler), so two requests go out. The server finds the first file of that minute and gives the
second one seconds in its name: `backup_2026-10-20_15-03.zip` and
`backup_2026-10-20_15-03-23.zip`, same size, written 1 ms apart (seen on a real installation).
Every manual backup takes twice the space, and with `maxBackups` the duplicates push older
backups out sooner. Automatic backups are not affected. Present since at least v1.2.0.

## Files
- `web/templates/backup.html` (`createBackupBtn`)
- `web/static/js/backup.js` (`BackupManager.init`, `createBackup`)

## Acceptance criteria
- [ ] One click on "Create backup" sends one request and adds one file to the list.
- [ ] A second click while the first backup is running sends nothing.
- [ ] The other buttons on the page (save settings, refresh) also run once per click.
