# House Description

House description for the AI agent. Fill in this file with information about your house.

## Rooms (areas)

| Room | Description |
|-------|------|
| Salon | Main living room |
| Kuchnia | Open kitchen connected to the living room |
| Biuro | Work room |
| Sypialnia | Master bedroom |
| Lazienka | Bathroom upstairs |
| Korytarz | Entrance hallway |

## Devices

Fill in the table with the devices in each room.

| Room | Device | Type | Notes |
|-------|-----------|-----|-------|
| Salon | Swiatlo glowne | switch | - |
| Salon | LED sufit | dimmer | 0-100% |
| Kuchnia | Swiatlo blat | dimmer | 0-100% |

## Rules

- Do not turn off the hallway light between 22:00 and 06:00
- Close the blinds at sunset
- Never set the heat recovery ventilator to 0 when the temperature is < 5 C

## Scenes

| Scene | Description |
|-------|------|
| Film | Living room: LED 20%, blinds closed |
| Dobranoc | Everything OFF, blinds closed |
| Poranek | Blinds open, kitchen LED 80% |
