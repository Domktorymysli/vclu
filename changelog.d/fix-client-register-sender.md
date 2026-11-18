### Grenton

- **Clients registered on vclu get reports after a change** - `SYSTEM:clientRegister` stored the subscription where the reporter did not look, so a client got only the answer with the values at registering and none of the changes after it. A client that registers with address 0 and port 0 gets its reports where it sent the request from, as from a CLU; the same for `clientDestroy(0,0,...)`.
