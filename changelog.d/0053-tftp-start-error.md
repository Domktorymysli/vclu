### Grenton

- **Object Manager is told when the TFTP server cannot start** - when the TFTP port is taken (another TFTP server, dnsmasq) or vCLU may not bind it, `req_start_ftp` and `StartTFTPd` get no OK, so Object Manager no longer sends a project into nothing, and the next request tries again instead of being answered OK until a restart ([#53](https://github.com/Domktorymysli/vclu/issues/53)).
