### Web

- **Dashboards in the viewer's language** - every text a dashboard draws itself (states such as "on" and "closed", buttons, the calendar, the forecast, the energy flow, the PIN page, loading and errors) comes from the `dashboard` section of `web/locales/` in the language vCLU picked for the viewer: `?lang=`, then the cookie, then `Accept-Language`, as on the other pages. `<html lang>`, dates, times and decimal separators follow it instead of the browser's language. Labels written in Lua are shown as written ([#85](https://github.com/Domktorymysli/vclu/issues/85)).
