### Grenton

- **Object Manager 1.13 resets vCLU after sending a project** - with `UseCloud` on in the project, OM tried to set up the Grenton cloud, failed with "Konfiguracja połączenia CLU do chmury nie powiodła się" and did not reset the CLU, so the new configuration was not loaded. vCLU now answers OM's cloud certificate read, and OM resets it ([#152](https://github.com/Domktorymysli/vclu/issues/152)).
