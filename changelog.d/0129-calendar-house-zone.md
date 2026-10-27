### Dashboards

- **Calendar and forecast times follow the house's clock without `setTimezone`** - the calendar and forecast widgets now get the server's zone name (from `TZ`, else the `/etc/localtime` link), so an event at 18:00 shows as 18:00 in a browser in another zone; when no name can be found they keep the browser's zone as before. `datetime.timezone()` still returns `""` without `setTimezone` ([#129](https://github.com/Domktorymysli/vclu/issues/129)).
