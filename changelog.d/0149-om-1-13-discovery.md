### Grenton

- **Object Manager 1.13 adds a vCLU found by discovery** - OM 1.13 gives a discovered CLU an address from the project's IP range, skipping addresses of other CLUs in the project (a vCLU reset in the web UI is a new CLU to OM, and its old entry keeps the address), and sends `null` when the range has none left. vCLU cannot move but confirmed every address, so OM moved the CLU to an address nobody had and SetKey failed ("Host is down", "Receive timed out"). vCLU now confirms only its own address and otherwise logs both addresses and what to change in OM: remove the old CLU from the project and make the IP range include vCLU's address ([#149](https://github.com/Domktorymysli/vclu/issues/149)).
- **More in the log for discovery problems** - the temporary IV of each discovery answer, which key a set-IP request came with, and for an unknown command its bytes escaped, with a hint when only the first 16 bytes are unreadable (the right key with another IV, e.g. after a second discovery) ([#149](https://github.com/Domktorymysli/vclu/issues/149)).

### Web

- **The setup wizard restarts vCLU when it finishes** - the page's restart request came right after the wizard turned the login on, got 401, and vCLU ran without its keys and the Grenton protocol until restarted by hand ([#149](https://github.com/Domktorymysli/vclu/issues/149)).
