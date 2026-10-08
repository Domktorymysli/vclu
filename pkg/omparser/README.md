# omparser

Parser i generator proxy dla plików Grenton `om.lua`.

## Co to robi?

1. **Parsuje** pliki `om.lua` generowane przez Object Manager
2. **Generuje** czysty kod Lua z klasami proxy zamiast `execute(index)`
3. **Generuje** JSON dla Home Assistant MQTT Discovery

## Użycie w Go

```go
import "github.com/Domktorymysli/vclu/pkg/omparser"

// Parsowanie om.lua
content, _ := os.ReadFile("om.lua")
parsed, err := omparser.Parse(string(content))

// Informacje o CLU
fmt.Printf("CLU: %s (%s)\n", parsed.CLU.ID, parsed.CLU.Name)
fmt.Printf("IP: %s\n", omparser.IPToString(parsed.CLU.IP))

// Lista obiektów
for _, obj := range parsed.GetObjectsByClass(omparser.ClassDOUT) {
    fmt.Printf("Switch: %s (%s)\n", obj.ID, obj.Name)
}

// Generowanie proxy Lua
config := omparser.DefaultConfig()
config.Namespace = "dom"
lua := omparser.GenerateProxy(parsed, config)
os.WriteFile("proxy.lua", []byte(lua), 0644)
```

## Wygenerowany kod Lua

Generator tworzy czytelne API zamiast numerycznych indeksów:

### Przed (oryginalny Grenton)
```lua
-- Włącz światło
DOU5048:execute(1, 0)

-- Pobierz stan
local value = DOU5048:get(0)

-- Ustaw pozycję rolety
ROL1234:execute(10, 50, 91)
```

### Po (z proxy)
```lua
-- Włącz światło
dom.P1_DACHOWE_BOK:switchOn()

-- Pobierz stan
local value = dom.P1_DACHOWE_BOK:getValue()

-- Ustaw pozycję rolety
dom.roleta_salon:setPosition(50)
```

## Klasy proxy

| Klasa            | Typ Grenton       | Metody                                                                                                                   |
|------------------|-------------------|--------------------------------------------------------------------------------------------------------------------------|
| `RemoteSwitch`   | DOUT              | `getValue()`, `setValue(v)`, `switchOn(time)`, `switchOff(time)`, `on()`, `off()`, `toggle()`                            |
| `RemoteSensor`   | DIN, AnalogIN     | `getValue()`                                                                                                             |
| `RemoteLight`    | DIMMER            | `getValue()`, `setValue(v, ramp)`, `getBrightness()`, `setBrightness(%, ramp)`, `on()`, `off()`, `toggle()`, `dim(ramp)` |
| `RemoteRGBLight` | LEDRGB            | jak Light + `getHue()`, `setHue(h, ramp)`, `getRGB()`, `setRGB(hex, ramp)`, `setColor(r,g,b, ramp)`                      |
| `RemoteCover`    | ROLLER, ROLLER_SH | `getPosition()`, `setPosition(pos, lamel)`, `open(time)`, `close(time)`, `stop()`, `up()`, `down()`                      |
| `RemoteNumber`   | AnalogOUT         | `getValue()`, `setValue(v)`, `getMin()`, `getMax()`                                                                      |

## Eventy

Każdy obiekt proxy obsługuje eventy:

```lua
-- Rejestracja callbacka
dom.przycisk_salon:onClick(function()
    dom.lampka_salon:toggle()
end)

dom.lampka_salon:onSwitchOn(function()
    print("Światło włączone!")
end)

dom.lampka_salon:onChange(function(newValue, oldValue)
    print("Zmiana: " .. oldValue .. " -> " .. newValue)
end)

-- Chainable API
dom.czujnik
    :onSwitchOn(function() print("ON") end)
    :onSwitchOff(function() print("OFF") end)
```

### Dostępne eventy

| Klasa            | Eventy                                                                      |
|------------------|-----------------------------------------------------------------------------|
| `RemoteSwitch`   | `onSwitchOn`, `onSwitchOff`, `onChange`                                     |
| `RemoteSensor`   | `onClick`, `onLongPress`, `onHold`, `onSwitchOn`, `onSwitchOff`, `onChange` |
| `RemoteLight`    | `onSwitchOn`, `onSwitchOff`, `onChange`                                     |
| `RemoteCover`    | `onUp`, `onDown`, `onStop`, `onPositionChange`, `onChange`                  |

## Registry i forwarding

Generator opcjonalnie tworzy registry do synchronizacji stanów z prawdziwych CLU:

```lua
-- W vCLU - aktualizacja stanu z prawdziwego CLU
sync("CLU212345678.DOU5048", 1)  -- wartość się zmieniła

-- Forwarding eventów
event("CLU212345678.DIN7929.OnClick")  -- przycisk kliknięty
```

Registry pozwala na lookup obiektów na 3 sposoby:

```lua
_registry["CLU212345678.DOU5048"]  -- po pełnej ścieżce
_registry["P1_DACHOWE_BOK"]       -- po friendly name
_registry["DOU5048"]               -- po ID obiektu
```

## Tagi i grupy

System tagów pozwala grupować obiekty i wykonywać operacje bulk:

### Dodawanie tagów

```lua
-- Chainable API
dom.P1_DACHOWE_BOK:addTag("swiatla", "parter", "zewnetrzne")
dom.P1_ELEKTROZACZEP:addTag("domofon", "brama", "furtka")
dom.P1_16_OUT_R10_LAZIENKA_SWIATLO:addTag("swiatla", "parter", "lazienka")
```

### Bulk operations

```lua
-- Wyłącz wszystkie światła
getByTag("swiatla"):off()

-- Wyłącz wszystko na parterze
getByTag("parter"):off()

-- Włącz wszystko co ma tag "zewnetrzne"
getByTag("zewnetrzne"):on()

-- Łączenie tagów (OR) - obiekty z tagiem "lazienka" LUB "kuchnia"
getByTags("lazienka", "kuchnia"):off()
```

### Iteracja i filtrowanie

```lua
-- Iteracja po grupie
getByTag("swiatla"):each(function(obj)
    print(obj._objId)
end)

-- Filtrowanie - wyłącz tylko te które są włączone
getByTag("swiatla"):filter(function(obj)
    return obj:getValue() == 1
end):off()

-- Ile obiektów ma dany tag
print(getByTag("swiatla"):count())
```

### Sprawdzanie tagów

```lua
-- Czy obiekt ma tag
if dom.P1_ELEKTROZACZEP:hasTag("domofon") then
    print("To jest domofon!")
end

-- Lista tagów obiektu
local tags = dom.P1_ELEKTROZACZEP:getTags()

-- Lista wszystkich tagów w systemie
local allTags = getAllTags()
```

### API tagów

| Metoda | Opis |
|--------|------|
| `obj:addTag(...)` | Dodaj tagi (chainable) |
| `obj:removeTag(tag)` | Usuń tag |
| `obj:hasTag(tag)` | Sprawdź czy ma tag |
| `obj:getTags()` | Lista tagów obiektu |
| `getByTag(tag)` | Pobierz grupę po tagu |
| `getByTags(...)` | Pobierz grupę po wielu tagach (OR) |
| `getAllTags()` | Lista wszystkich tagów |
| `group:count()` | Ile obiektów w grupie |
| `group:each(fn)` | Iteruj po grupie |
| `group:filter(fn)` | Filtruj grupę |

## Dynamiczne wywołania (API/MQTT)

System pozwala na bezpieczne dynamiczne wywoływanie metod - przydatne dla MQTT/REST API.

### Wywołanie metody po nazwie

```lua
-- Sprawdź czy metoda jest dostępna
if dom.lampka:canExecute("switchOn") then
    dom.lampka:call("switchOn")
end

-- Z argumentami
dom.roleta:call("setPosition", 50)
dom.led:call("setRGB", "#FF0000")
```

### Lista dozwolonych metod

Każdy typ obiektu ma whitelistę metod widocznych przez `getMethods()`:

```lua
dom.lampka:getMethods()
-- RemoteSwitch: {"getValue", "setValue", "switchOn", "switchOff", "on", "off", "toggle"}

dom.dimmer:getMethods()
-- RemoteLight: {"getValue", "setValue", "getBrightness", "setBrightness", "switchOn", "switchOff", "on", "off", "toggle", "dim"}

dom.roleta:getMethods()
-- RemoteCover: {"getState", "getPosition", "setPosition", "getTilt", "setTilt", "open", "close", "stop", "up", "down"}
```

### Informacje o obiekcie

```lua
dom.lampka:getInfo()
-- {
--   id = "DOU5048",
--   type = "DOUT",
--   tags = {"swiatla", "parter"},
--   methods = {"getValue", "setValue", "switchOn", "switchOff", "on", "off", "toggle"}
-- }
```

### Dozwolone metody per typ

| Typ | Metody |
|-----|--------|
| `RemoteSwitch` | `getValue`, `setValue`, `switchOn`, `switchOff`, `on`, `off`, `toggle` |
| `RemoteSensor` | `getValue` |
| `RemoteLight` | `getValue`, `setValue`, `getBrightness`, `setBrightness`, `switchOn`, `switchOff`, `on`, `off`, `toggle`, `dim` |
| `RemoteRGBLight` | jak Light + `getHue`, `setHue`, `getSaturation`, `setSaturation`, `getRGB`, `setRGB`, `setColor` |
| `RemoteCover` | `getState`, `getPosition`, `setPosition`, `getTilt`, `setTilt`, `open`, `close`, `stop`, `up`, `down` |
| `RemoteNumber` | `getValue`, `setValue`, `getMin`, `getMax` |

### Użycie w MQTT handler

```lua
-- Topic: grenton/.../dou5048/switchOn
-- Payload: (opcjonalnie argumenty jako JSON)
function onMqttCall(objId, method, argsJson)
    local obj = _registry[objId]
    if obj and obj:canExecute(method) then
        local args = argsJson and json.decode(argsJson) or {}
        obj:call(method, table.unpack(args))
    end
end
```

## Konfiguracja generatora

```go
config := omparser.GeneratorConfig{
    Namespace:          "salon",  // nazwa namespace w Lua
    IncludeBaseClasses: true,     // dołącz definicje klas
    IncludeRegistry:    true,     // dołącz registry do forwardingu
}

lua := omparser.GenerateProxy(parsed, config)
```

Jeśli masz wiele CLU, możesz wygenerować klasy tylko raz:

```go
// Pierwszy CLU - pełny kod
config1 := omparser.DefaultConfig()
config1.Namespace = "parter"
lua1 := omparser.GenerateProxy(parsed1, config1)

// Kolejne CLU - bez klas bazowych (już są zdefiniowane)
config2 := omparser.GeneratorConfig{
    Namespace:          "pietro",
    IncludeBaseClasses: false,
    IncludeRegistry:    true,
}
lua2 := omparser.GenerateProxy(parsed2, config2)
```

## Home Assistant MQTT Discovery

Generator JSON dla automatycznego wykrywania urządzeń w Home Assistant.

### Użycie

```go
config := omparser.HADiscoveryConfig{
    TopicPrefix:     "grenton",      // prefix dla topiców MQTT
    DiscoveryPrefix: "homeassistant", // prefix HA Discovery
    DeviceName:      "DOM20",         // nazwa urządzenia w HA
}

// Lista wiadomości do wysłania
messages := omparser.GenerateHADiscovery(parsed, config)

for _, msg := range messages {
    mqtt.Publish(msg.Topic, msg.Payload, msg.Retain)
}

// Lub jako JSON (do debugowania)
jsonStr, _ := omparser.GenerateHADiscoveryJSON(parsed, config)
```

### Struktura wiadomości

```go
type HADiscoveryMessage struct {
    Topic   string                 // np. "homeassistant/switch/grenton_clu_dou5048/config"
    Payload map[string]interface{} // JSON payload dla HA
    Retain  bool                   // zawsze true dla discovery
}
```

### Przykład wygenerowanego JSON (switch)

```json
{
  "topic": "homeassistant/switch/grenton_clu212345678_dou5048/config",
  "payload": {
    "name": "P1_DACHOWE_BOK",
    "unique_id": "grenton_clu212345678_dou5048",
    "command_topic": "grenton/clu212345678/switch/dou5048/set",
    "state_topic": "grenton/clu212345678/switch/dou5048/state",
    "payload_on": "ON",
    "payload_off": "OFF",
    "device": {
      "identifiers": ["clu212345678"],
      "name": "DOM20",
      "manufacturer": "Grenton",
      "model": "CLU"
    }
  },
  "retain": true
}
```

### Mapowanie typów Grenton → HA

| Typ Grenton       | Komponent HA    | Topiki                                      |
|-------------------|-----------------|---------------------------------------------|
| DOUT              | `switch`        | `set`, `state`                              |
| DIN               | `binary_sensor` | `state`                                     |
| DIMMER            | `light`         | `set`, `state`, `brightness/set`, `brightness` |
| LEDRGB            | `light` (JSON)  | `set`, `state` (JSON z rgb/brightness)      |
| ROLLER, ROLLER_SH | `cover`         | `set`, `state`, `position/set`, `position`  |
| AnalogIN          | `sensor`        | `state`                                     |
| AnalogOUT         | `number`        | `set`, `state`                              |

### Struktura topiców

```
grenton/{clu_id}/{component}/{object_id}/set      # komendy z HA
grenton/{clu_id}/{component}/{object_id}/state    # stan do HA
grenton/{clu_id}/{component}/{object_id}/brightness/set  # dla dimmerów
```

Przykłady:
- `grenton/clu212345678/switch/dou5048/set` → "ON" / "OFF"
- `grenton/clu212345678/switch/dou5048/state` → "ON" / "OFF"
- `grenton/clu212345678/cover/rol1234/set` → "OPEN" / "CLOSE" / "STOP"
- `grenton/clu212345678/cover/rol1234/position/set` → 0-100

## Struktura sparsowanych danych

```go
type ParsedOM struct {
    CLU        CLUInfo                  // info o CLU
    References []CLUReference           // referencje do innych CLU
    Modules    []Module                 // moduły fizyczne
    Objects    []GrentonObject          // obiekty I/O
    Events     []EventHandler           // handlery eventów
    ObjectMap  map[string]GrentonObject // szybki lookup po ID
}

// Filtrowanie po typie
switches := parsed.GetObjectsByClass(omparser.ClassDOUT)
sensors := parsed.GetObjectsByClass(omparser.ClassDIN)
exposable := parsed.GetExposableObjects()  // wszystkie do MQTT
```

## Typy obiektów

| Stała              | Wartość   | Opis                 |
|--------------------|-----------|----------------------|
| `ClassDIN`         | 3         | Digital Input        |
| `ClassDOUT`        | 4         | Digital Output       |
| `ClassDIMMER`      | 5         | Dimmer               |
| `ClassLEDRGB`      | 9         | RGB LED              |
| `ClassROLLER_SH`   | 11        | Roller (bez pozycji) |
| `ClassAnalogIN`    | 12        | Analog Input         |
| `ClassAnalogOUT`   | 13        | Analog Output        |
| `ClassROLLER`      | 24        | Roller (z pozycją)   |

## Helper functions

Generator opcjonalnie dołącza funkcje pomocnicze (`IncludeHelpers: true`).

### Math helpers

```lua
-- Clamp - ograniczenie wartości do zakresu
clamp(15, 0, 10)  -- zwraca 10 (max)
clamp(-5, 0, 10)  -- zwraca 0 (min)
clamp(5, 0, 10)   -- zwraca 5 (bez zmian)

-- Lerp (Linear interpolation) - interpolacja liniowa
-- Przydatne do płynnych przejść między wartościami
lerp(0, 100, 0)     -- zwraca 0 (start)
lerp(0, 100, 1)     -- zwraca 100 (koniec)
lerp(0, 100, 0.5)   -- zwraca 50 (środek)
lerp(0, 100, 0.25)  -- zwraca 25 (25% drogi)

-- Map - przekształcenie wartości z jednego zakresu na inny
-- Np. czujnik 0-1023 → jasność 0-100%
map(512, 0, 1023, 0, 100)  -- zwraca ~50

-- Round - zaokrąglenie do podanej precyzji
round(3.14159)      -- zwraca 3
round(3.14159, 2)   -- zwraca 3.14
```

### Timery

```lua
-- Wykonaj po czasie (ms)
after(5000, function()
    dom.lampka:off()
end)

-- Wykonuj cyklicznie
local id = every(60000, function()
    print("Minuta!")
end)

-- Anuluj timer
cancel(id)
```

### Sceny

```lua
-- Definicja sceny
scene("kino", function()
    dom.tv:on()
    dom.rolety_salon:close()
    getByTag("swiatla_salon"):off()
    dom.lampka_kino:setBrightness(20)
end)

scene("wyjscie", function()
    getByTag("swiatla"):off()
    getByTag("rolety"):close()
end)

-- Uruchamianie sceny
scenes.kino:run()
-- lub
runScene("kino")
-- lub
getScene("kino"):run()

-- Włączanie/wyłączanie scen
scenes.kino:disable()
scenes.kino:enable()
if scenes.kino:isEnabled() then ... end

-- Lista wszystkich scen
local names = getAllScenes()  -- {"kino", "wyjscie", ...}
```

### Sekwencje

```lua
-- Sekwencja akcji z opóźnieniami
sequence()
    :add(function() dom.lampka1:on() end)
    :wait(1000)
    :add(function() dom.lampka2:on() end)
    :wait(1000)
    :add(function() dom.lampka3:on() end)
    :run(function()
        print("Sekwencja zakończona!")
    end)
```

### Cascade (efekt kaskadowy)

```lua
-- Włącz światła po kolei z opóźnieniem 200ms
getByTag("swiatla"):cascade(200, function(obj, i)
    obj:on()
end)

-- Wyłącz światła kaskadowo
cascade(getByTag("swiatla"), 100, function(obj)
    obj:off()
end)
```

### Zapis/odtworzenie stanu

```lua
-- Zapisz stan obiektu
local state = dom.lampka:saveState()

-- Później przywróć
dom.lampka:restoreState(state)

-- Dla grupy
local states = getByTag("swiatla"):saveState()
-- ...
getByTag("swiatla"):restoreState(states)
```

### Chainable delay

```lua
-- Wykonaj akcję na obiekcie po czasie
dom.lampka
    :on()
    :after(5000, function(self)
        self:off()
    end)
```

### Logging

```lua
-- Poziomy logowania
log.level = "debug"  -- debug, info, warn, error

log.debug("Szczegóły: %s", value)
log.info("Włączono: %s", obj._objId)
log.warn("Uwaga: %d prób", count)
log.error("Błąd: %s", err)
```

---

## Moduły Runtime

### MQTT Module

Multi-instance MQTT client z dwoma stylami API:
- **Grenton-compatible** - numeryczne indeksy (`:get(index)`, `:set(index, value)`, `:execute(action)`)
- **Modern chainable** - czytelne metody z chainable API

#### Tworzenie instancji

```lua
-- Instancja dla Home Assistant
mqtt_ha = MQTT:new("home_assistant")
mqtt_ha:setUrl("tcp://192.168.1.100:1883")
       :setClientId("vclu_ha")
       :setTopicPrefix("grenton")
       :connect()

-- Instancja dla chmury
mqtt_cloud = MQTT:new("cloud")
mqtt_cloud:setUrl("ssl://mqtt.cloud.example.com:8883")
          :setCredentials("user", "secret_token")
          :setCleanSession(false)
          :connect()
```

#### API

```lua
-- Konfiguracja chainable
mqtt:setUrl("tcp://broker:1883")
    :setPort(1883)
    :setClientId("my_client")
    :setCredentials("user", "pass")
    :setKeepAlive(60)
    :setCleanSession(true)
    :setQoS(1)
    :setTopicPrefix("grenton")

-- Połączenie
mqtt:connect()
mqtt:disconnect()
mqtt:reconnect()

-- Publikowanie
mqtt:publish("switch/light1/state", "ON")
mqtt:publish("sensor/temp", "22.5", 1, true)  -- qos=1, retain=true
mqtt:publishRetain("config/device", payload)  -- skrót dla retain=true

-- Subskrypcje
mqtt:subscribe("home/+/set", function(topic, payload)
    print("Received:", topic, payload)
end)

mqtt:subscribe({
    "home/switch/+/set",
    "home/light/+/set",
    "home/cover/+/set"
}, handler)

mqtt:unsubscribe("home/switch/+/set")

-- Callbacki
mqtt:onConnect(function()
    print("Connected!")
    mqtt:publishAllDiscovery()
end)

mqtt:onDisconnect(function(reason)
    print("Disconnected:", reason)
end)

mqtt:onMessage(function(topic, payload)
    print("Message:", topic, payload)
end)
```

#### HA Discovery

Discovery messages są generowane w Go (`generator_ha.go`) i przekazywane do Lua:

```lua
-- Ładowanie wiadomości discovery z Go
mqtt:loadDiscoveryMessages(messagesFromGo)

-- Publikowanie wszystkich discovery (retained)
mqtt:publishAllDiscovery()

-- Sprawdzenie statusu
local count = mqtt:getDiscoveryCount()
print("Discovery messages:", count)
```

#### Właściwości instancji

| Właściwość | Getter | Setter | Opis |
|------------|--------|--------|------|
| Url | `getUrl()` | `setUrl(url)` | URL brokera |
| Port | `getPort()` | `setPort(port)` | Port (domyślnie 1883) |
| ClientId | `getClientId()` | `setClientId(id)` | ID klienta |
| Username | `getUsername()` | `setCredentials(user, pass)` | Użytkownik |
| Password | - | `setCredentials(user, pass)` | Hasło |
| KeepAlive | `getKeepAlive()` | `setKeepAlive(sec)` | Keep-alive (domyślnie 60s) |
| CleanSession | `getCleanSession()` | `setCleanSession(bool)` | Clean session |
| QoS | `getQoS()` | `setQoS(0\|1\|2)` | Domyślny QoS |
| TopicPrefix | `getTopicPrefix()` | `setTopicPrefix(prefix)` | Prefix topiców |
| Connected | `isConnected()` | - | Status połączenia |

---

### MqttHandler

Automatyczne tłumaczenie komend z Home Assistant na wywołania metod Lua:

```lua
-- Inicjalizacja
handler = MqttHandler:new(mqtt_ha, _registry)
handler:start()

-- Handler automatycznie subskrybuje:
-- - {prefix}/+/+/set           (komendy ON/OFF/OPEN/CLOSE/STOP)
-- - {prefix}/+/+/brightness/set (jasność dimmerów)
-- - {prefix}/+/+/position/set   (pozycja rolet)
-- - {prefix}/+/+/rgb/set        (kolor RGB)
```

#### Mapowanie komend

| Komponent | Payload | Metoda Lua |
|-----------|---------|------------|
| switch | `ON` | `obj:switchOn()` |
| switch | `OFF` | `obj:switchOff()` |
| light | `ON` | `obj:on()` |
| light | `OFF` | `obj:off()` |
| light brightness | `0-255` | `obj:setBrightness(value * 100 / 255)` |
| cover | `OPEN` | `obj:open()` |
| cover | `CLOSE` | `obj:close()` |
| cover | `STOP` | `obj:stop()` |
| cover position | `0-100` | `obj:setPosition(value)` |
| number | `float` | `obj:setValue(value)` |

#### Publikowanie stanu

```lua
-- Publikuj stan pojedynczego obiektu
handler:publishState(obj, "switch")

-- Automatyczna publikacja przy zmianie
obj:onChange(function(newValue)
    handler:publishState(obj, "switch")
end)
```

---

### HTTP Module

Multi-instance HTTP client z sync/async API:

#### Tworzenie instancji

```lua
-- REST API
api = HTTP:new("my_api")
api:setBaseUrl("https://api.example.com/v1")
   :setBearerToken("your_token_here")
   :setTimeout(5000)

-- Webhook
webhook = HTTP:new("webhook")
webhook:setBaseUrl("https://webhook.site")
       :setHeader("X-Custom-Header", "value")
```

#### Grenton-compatible API

```lua
-- Indeksy właściwości
HTTP:set(0, "https://api.example.com")  -- Host
HTTP:set(1, "GET")                       -- RequestType
HTTP:set(2, "")                          -- Body
HTTP:set(3, 5000)                        -- Timeout (ms)
HTTP:set(4, true)                        -- VerifySSL
HTTP:set(5, true)                        -- FollowRedirects

-- Wykonanie żądania
HTTP:execute(0, "/users")  -- RequestStart(path)
```

#### Modern Sync API

```lua
-- GET
local response = api:get("/users")
local response = api:get("/users", {["Authorization"] = "Bearer xxx"})

-- POST
local response = api:post("/users", '{"name":"John"}')
local response = api:postJSON("/users", {name = "John"})  -- auto JSON encode

-- PUT/PATCH/DELETE
local response = api:put("/users/1", body)
local response = api:patch("/users/1", body)
local response = api:delete("/users/1")

-- Uniwersalna metoda
local response = api:request("POST", "/users", body, headers)

-- Odpowiedź
if response.ok then
    print("Status:", response.status)       -- 200
    print("Body:", response.body)           -- string
    print("Headers:", response.headers)     -- table
else
    print("Error:", response.error)
end
```

#### Async API

```lua
-- Async GET
api:asyncGet("/users", function(response, err)
    if not err then
        print(response.body)
    else
        print("Error:", err)
    end
end)

-- Async POST
api:asyncPost("/users", body, function(response, err)
    -- handle response
end)

-- Async JSON POST
api:asyncPostJSON("/users", {name = "John"}, function(response, err)
    -- handle response
end)
```

#### Konfiguracja

```lua
-- Base URL
api:setBaseUrl("https://api.example.com/v1")

-- Autentykacja
api:setBearerToken("token")
api:setBasicAuth("username", "password")

-- Nagłówki
api:setHeader("X-API-Key", "key123")
api:setHeader("Content-Type", "application/json")

-- Timeout (ms)
api:setTimeout(10000)

-- SSL/Redirects
api:setVerifySSL(true)
api:setFollowRedirects(true)
```

#### Właściwości instancji

| Właściwość      | Getter                 | Setter                     | Opis                     |
|-----------------|------------------------|----------------------------|--------------------------|
| BaseUrl         | `getBaseUrl()`         | `setBaseUrl(url)`          | Bazowy URL               |
| Timeout         | `getTimeout()`         | `setTimeout(ms)`           | Timeout w ms             |
| VerifySSL       | `getVerifySSL()`       | `setVerifySSL(bool)`       | Weryfikacja SSL          |
| FollowRedirects | `getFollowRedirects()` | `setFollowRedirects(bool)` | Podążanie za redirectami |
| Headers         | `getHeaders()`         | `setHeader(k,v)`           | Domyślne nagłówki        |

---

### Parsers (JSON, XML, DateTime)

Moduły parserów do przetwarzania danych z HTTP API i innych źródeł.

#### JSON Parser

```lua
-- Dekodowanie JSON na tabelę Lua
local data = json.decode('{"name":"John","age":30,"active":true}')
print(data.name)    -- "John"
print(data.age)     -- 30
print(data.active)  -- true

-- Tablice
local arr = json.decode('[1, 2, 3, "four"]')
print(arr[1])  -- 1
print(arr[4])  -- "four"

-- Zagnieżdżone struktury
local nested = json.decode('{"user":{"name":"John","roles":["admin","user"]}}')
print(nested.user.name)     -- "John"
print(nested.user.roles[1]) -- "admin"

-- Kodowanie tabeli Lua na JSON
local jsonStr = json.encode({name = "John", age = 30})
-- '{"age":30,"name":"John"}'
```

#### XML Parser

```lua
-- Prosty XML
local data = xml.decode('<user><name>John</name><age>30</age></user>')
print(data.user.name)  -- "John"
print(data.user.age)   -- "30"

-- Atrybuty (dostępne jako _attr)
local xml_with_attrs = xml.decode('<device id="123" type="switch"><name>Light</name></device>')
print(xml_with_attrs.device._attr.id)    -- "123"
print(xml_with_attrs.device._attr.type)  -- "switch"
print(xml_with_attrs.device.name)        -- "Light"

-- Elementy z tekstem i atrybutami (tekst jako _text)
local mixed = xml.decode('<item price="100">Widget</item>')
print(mixed.item._attr.price)  -- "100"
print(mixed.item._text)        -- "Widget"

-- Powtarzające się elementy (jako tablica)
local list = xml.decode('<items><item>A</item><item>B</item><item>C</item></items>')
print(#list.items.item)    -- 3
print(list.items.item[1])  -- "A"
print(list.items.item[2])  -- "B"
```

#### DateTime Parser

Parser czasu obsługujący wiele formatów - rozwiązuje problem konwersji dat w Grentonie.

##### Strefa czasowa

Zegar serwera zwykle chodzi w UTC. Ustaw strefę raz, w pierwszej linii `modules/init.lua`;
wtedy `now`, `fromTimestamp`, `parse` (napisy bez strefy), `format`, `add` i `diff`
liczą w czasie lokalnym, razem ze zmianą czasu. Za tą strefą idą też cechy czasu CLU vCLU
(`CLU.FEATURE_HOUR` itd.). `os.date` zostaje czasem serwera.

```lua
datetime.setTimezone("Europe/Warsaw")
print(datetime.timezone())         -- "Europe/Warsaw" ("" = nie ustawiono)
local d = datetime.now()
print(d.hour, d.tz, d.tzOffset)     -- 22  CEST  7200
```

Po przeładowaniu runtime strefa jest ustawiana ponownie przez `init.lua`.

##### Parsowanie różnych formatów

```lua
-- ISO 8601
local d = datetime.parse("2024-01-15T10:30:00Z")
local d = datetime.parse("2024-01-15T10:30:00+01:00")

-- Daty
local d = datetime.parse("2024-01-15")        -- ISO
local d = datetime.parse("15-01-2024")        -- europejski
local d = datetime.parse("01/15/2024")        -- amerykański
local d = datetime.parse("15.01.2024")        -- z kropkami

-- Data i czas
local d = datetime.parse("2024-01-15 10:30:00")
local d = datetime.parse("2024-01-15 10:30")

-- Tylko czas
local d = datetime.parse("10:30:00")
local d = datetime.parse("10:30")

-- Słowne formaty
local d = datetime.parse("Jan 15, 2024")
local d = datetime.parse("January 15, 2024")
local d = datetime.parse("15 Jan 2024")

-- Unix timestamp
local d = datetime.parse("1705315800")
```

##### Dostęp do komponentów

```lua
local d = datetime.parse("2024-01-15 10:30:45")

-- Komponenty daty
print(d.year)       -- 2024
print(d.month)      -- 1
print(d.day)        -- 15

-- Komponenty czasu
print(d.hour)       -- 10
print(d.minute)     -- 30
print(d.second)     -- 45

-- Dodatkowe informacje
print(d.weekday)     -- 1 (0=Niedziela, 1=Poniedziałek, ...)
print(d.yearday)     -- 15 (dzień roku)
print(d.monthName)   -- "January"
print(d.weekdayName) -- "Monday"
print(d.tzOffset)    -- "+0100"

-- Gotowe formaty
print(d.iso)         -- "2024-01-15T10:30:45+01:00"
print(d.date)        -- "2024-01-15"
print(d.time)        -- "10:30:45"

-- Unix timestamp
print(d.timestamp)   -- 1705315845 (sekundy)
print(d.timestampMs) -- 1705315845000 (milisekundy)
```

##### Aktualny czas

```lua
local now = datetime.now()
print(now.year, now.month, now.day)
print(now.hour, now.minute, now.second)
print(now.iso)
```

##### Z Unix timestamp

```lua
local d = datetime.fromTimestamp(1705315800)
print(d.iso)  -- "2024-01-15T..."
```

##### Formatowanie

```lua
local d = datetime.parse("2024-01-15 10:30:00")

-- Predefiniowane formaty
print(datetime.format(d, "iso"))    -- "2024-01-15T10:30:00+01:00"
print(datetime.format(d, "date"))   -- "2024-01-15"
print(datetime.format(d, "time"))   -- "10:30:00"

-- Własne formaty (Go-style)
print(datetime.format(d, "02.01.2006"))         -- "15.01.2024"
print(datetime.format(d, "2006/01/02 15:04"))   -- "2024/01/15 10:30"
print(datetime.format(d, "Monday, January 2"))  -- "Monday, January 15"
```

##### Różnica między datami

```lua
local d1 = datetime.parse("2024-01-15 10:00:00")
local d2 = datetime.parse("2024-01-15 12:30:00")

local diff = datetime.diff(d1, d2)  -- różnica w sekundach
print(diff)  -- 9000 (2.5 godziny)

-- Konwersja na inne jednostki
print(diff / 60)        -- minuty: 150
print(diff / 3600)      -- godziny: 2.5
print(diff / 86400)     -- dni: ~0.1
```

##### Dodawanie czasu

```lua
local now = datetime.now()

-- Dodaj 7 dni
local nextWeek = datetime.add(now, {days = 7})

-- Dodaj 2 godziny i 30 minut
local later = datetime.add(now, {hours = 2, minutes = 30})

-- Odejmij (wartości ujemne)
local yesterday = datetime.add(now, {days = -1})
local lastMonth = datetime.add(now, {months = -1})

-- Dostępne jednostki: years, months, days, hours, minutes, seconds
local future = datetime.add(now, {
    years = 1,
    months = 2,
    days = 15,
    hours = 3,
    minutes = 30,
    seconds = 45
})
```

##### Przykłady użycia w automatyce

```lua
-- Sprawdź czy jest noc (po 22:00 lub przed 6:00)
local now = datetime.now()
local isNight = now.hour >= 22 or now.hour < 6

-- Sprawdź czy weekend
local isWeekend = now.weekday == 0 or now.weekday == 6

-- Timer na konkretną godzinę
local targetTime = datetime.parse("2024-01-15 18:00:00")
local delay = datetime.diff(datetime.now(), targetTime) * 1000
if delay > 0 then
    after(delay, function()
        getByTag("swiatla_zewnetrzne"):on()
    end)
end

-- Parsowanie czasu z API
local apiResponse = http:get("/schedule")
local data = json.decode(apiResponse.body)
local eventTime = datetime.parse(data.nextEvent)
print("Następne zdarzenie:", eventTime.iso)
```

#### Color Parser

Moduł do konwersji kolorów między formatami - przydatny dla LED RGB, Philips Hue, WLED.

##### Konwersja Hex ↔ RGB

```lua
-- Hex na RGB
local rgb = color.hexToRGB("#FF5500")
print(rgb.r, rgb.g, rgb.b)  -- 255, 85, 0

-- Krótki format
local rgb = color.hexToRGB("#F00")  -- czerwony
local rgb = color.hexToRGB("FF5500")  -- bez #

-- RGB na Hex
local hex = color.rgbToHex(255, 85, 0)  -- "#FF5500"
local hex = color.rgbToHex({r=255, g=85, b=0})  -- z tabeli
```

##### Konwersja HSV (Hue/Saturation/Value)

```lua
-- HSV na RGB (h=0-360, s=0-100, v=0-100)
local rgb = color.hsvToRGB(0, 100, 100)   -- czerwony
local rgb = color.hsvToRGB(120, 100, 100) -- zielony
local rgb = color.hsvToRGB(240, 100, 100) -- niebieski

-- RGB na HSV
local hsv = color.rgbToHSV(255, 0, 0)
print(hsv.h, hsv.s, hsv.v)  -- 0, 100, 100

-- Z tabeli
local hsv = color.rgbToHSV({r=255, g=165, b=0})  -- pomarańczowy
print(hsv.h)  -- ~38
```

##### Konwersja HSL (Hue/Saturation/Lightness)

```lua
-- HSL na RGB (h=0-360, s=0-100, l=0-100)
local rgb = color.hslToRGB(0, 100, 50)  -- czerwony

-- RGB na HSL
local hsl = color.rgbToHSL(255, 0, 0)
print(hsl.h, hsl.s, hsl.l)  -- 0, 100, 50
```

##### Temperatura koloru (Kelvin)

Przydatne dla białych świateł z regulacją temperatury.

```lua
-- Ciepła biel (2700K)
local warm = color.kelvinToRGB(2700)
-- r=255, g~180, b~87

-- Neutralna biel (4000K)
local neutral = color.kelvinToRGB(4000)

-- Zimna biel / światło dzienne (6500K)
local cool = color.kelvinToRGB(6500)
-- r=255, g~254, b~250

-- Typowe wartości:
-- 1800K - świeca
-- 2700K - żarówka tradycyjna (ciepła)
-- 3000K - ciepła biel LED
-- 4000K - neutralna biel
-- 5500K - światło słoneczne
-- 6500K - światło dzienne (zimna)
```

##### Kolory po nazwie

```lua
-- Z nazwy na RGB (50+ kolorów CSS)
local orange = color.fromName("orange")
print(orange.r, orange.g, orange.b)  -- 255, 165, 0

-- RGB na nazwę (najbliższy dopasowany kolor)
local name = color.toName(255, 0, 0)  -- "red"
local name = color.toName({r=0, g=128, b=0})  -- "green"

-- Lista wszystkich dostępnych nazw
local names = color.getNames()

-- Dostępne nazwy kolorów:
-- black, white, red, green, blue, yellow, cyan, magenta,
-- orange, pink, purple, violet, brown, gray/grey, silver,
-- gold, navy, teal, olive, maroon, lime, coral, salmon,
-- tomato, crimson, indigo, turquoise, skyblue, lavender,
-- beige, ivory, khaki, chocolate, warmwhite, coolwhite,
-- darkred, darkgreen, darkblue, lightgreen, lightblue...
```

##### Manipulacja kolorami

```lua
-- Jasność (przyciemnianie)
local dimmed = color.setBrightness("#FF0000", 50)  -- 50% jasności
print(dimmed.hex)  -- ~#7F0000

local dim30 = color.setBrightness({r=255, g=165, b=0}, 30)

-- Mieszanie kolorów
local mixed = color.blend("#FF0000", "#0000FF", 0.5)  -- 50% mix
print(mixed.hex)  -- #7F007F (purpurowy)

local sunrise = color.blend("orange", "yellow", 0.3)

-- Kolor dopełniający (180° na kole barw)
local comp = color.complement("#FF0000")
print(comp.hex)  -- #00FFFF (cyan)

-- Inwersja koloru
local inv = color.invert("#FF5500")
print(inv.hex)  -- #00AAFF
```

##### Auto-parsing

```lua
-- Automatyczne wykrywanie formatu
local c = color.parse("#FF5500")      -- hex
local c = color.parse("rgb(255,85,0)") -- format CSS
local c = color.parse("orange")        -- nazwa

-- Wynik zawsze zawiera:
-- c.r, c.g, c.b - komponenty RGB
-- c.hex - string hex
-- c.format - wykryty format ("hex", "rgb", "name")
```

##### Przykłady użycia w automatyce

```lua
-- LED RGB strip - płynne przejście kolorów
local startColor = color.fromName("red")
local endColor = color.fromName("blue")

-- Gradient 10 kroków
for i = 0, 9 do
    local ratio = i / 9
    local step = color.blend(startColor, endColor, ratio)
    -- Wyślij step.r, step.g, step.b do LED
end

-- Światło nocne - ciepłe i przyciemnione
local nightLight = color.kelvinToRGB(2200)
local dimmed = color.setBrightness(nightLight, 20)  -- 20% jasności

-- Parsowanie koloru z Home Assistant
local haColor = color.parse(mqtt_payload)
local hsv = color.rgbToHSV(haColor.r, haColor.g, haColor.b)

-- Zmiana jasności zachowując kolor
local newColor = color.hsvToRGB(hsv.h, hsv.s, 50)  -- 50% jasności

-- Automatyczne oświetlenie według pory dnia
local hour = datetime.now().hour
local kelvin
if hour >= 22 or hour < 6 then
    kelvin = 2200  -- noc - bardzo ciepłe
elseif hour >= 6 and hour < 9 then
    kelvin = 4000  -- poranek - neutralne
elseif hour >= 9 and hour < 18 then
    kelvin = 5500  -- dzień - chłodniejsze
else
    kelvin = 3000  -- wieczór - ciepłe
end
local rgb = color.kelvinToRGB(kelvin)
```

---

### Timer System

Timery są zarządzane przez Go backend z limitami bezpieczeństwa:

#### Limity

| Limit         | Wartość  | Opis                                |
|---------------|----------|-------------------------------------|
| MaxTimers     | 1000     | Maksymalna liczba aktywnych timerów |
| MinTimeoutMs  | 1ms      | Minimalny timeout dla `after()`     |
| MinIntervalMs | 10ms     | Minimalny interwał dla `every()`    |
| MaxIntervalMs | 24h      | Maksymalny interwał                 |

#### Użycie

```lua
-- Jednorazowe wykonanie po czasie
local id = after(5000, function()
    dom.lampka:off()
end)

-- Cykliczne wykonanie
local id = every(60000, function()
    print("Minuta minęła!")
end)

-- Anulowanie timera
cancel(id)

-- Low-level TIMERS API
local id = TIMERS:setTimeout(5000, callback)
local id = TIMERS:setInterval(1000, callback)
TIMERS:clearTimer(id)
```

#### Statystyki (Go)

```go
stats := engine.GetTimerStats()
// TimerStats{
//     ActiveTimers:   45,
//     TotalCreated:   120,
//     TotalFired:     75,
//     TotalCancelled: 30,
//     Rejected:       2,
// }
```

---

## Architektura

```
┌─────────────────────────────────────────────────────────────────┐
│                          Lua                                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │   MQTT   │  │   HTTP   │  │  TIMERS  │  │  Proxy Classes   │ │
│  │ (multi)  │  │ (multi)  │  │          │  │  RemoteSwitch    │ │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  │  RemoteLight     │ │
│       │             │             │        │  RemoteCover     │ │
└───────┼─────────────┼─────────────┼────────┴──────────────────┴─┘
        │             │             │
        ▼             ▼             ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Go Backend                                 │
│  __go_mqtt_*    __go_http_*    __go_setTimeout/Interval         │
└─────────────────────────────────────────────────────────────────┘
        │             │
        ▼             ▼
   MQTT Broker    HTTP APIs
   (HA, Cloud)    (REST, etc.)
```

### Go Backend Functions

**MQTT:**
- `__go_mqtt_connect(instanceId, url, port, clientId, user, pass, keepAlive, cleanSession)`
- `__go_mqtt_disconnect(instanceId)`
- `__go_mqtt_publish(instanceId, topic, payload, qos, retain)`
- `__go_mqtt_subscribe(instanceId, topic)`

**HTTP:**
- `__go_http_request(instanceId, method, url, headers, body, timeout, verifySSL, followRedirects)`
- `__go_http_async_request(instanceId, requestId, method, url, headers, body, timeout, verifySSL, followRedirects)`

**Timers:**
- `__go_setTimeout(callback, ms)`
- `__go_setInterval(callback, ms)`
- `__go_clearTimer(id)`

---

## Przykład: Kompletna integracja z Home Assistant

```lua
-- 1. Parsowanie om.lua i generowanie proxy (w Go)
-- parsed := omparser.Parse(omContent)
-- proxyLua := omparser.GenerateProxy(parsed, config)
-- discoveryMsgs := omparser.GenerateHADiscovery(parsed, haConfig)

-- 2. Inicjalizacja MQTT
mqtt_ha = MQTT:new("home_assistant")
mqtt_ha:setUrl("tcp://192.168.1.100:1883")
       :setClientId("vclu_dom")
       :setTopicPrefix("grenton")

-- 3. Ładowanie discovery z Go
mqtt_ha:loadDiscoveryMessages(discoveryMsgs)

-- 4. Po połączeniu - publikuj discovery
mqtt_ha:onConnect(function()
    log.info("Connected to HA MQTT broker")
    mqtt_ha:publishAllDiscovery()
end)

-- 5. Inicjalizacja handlera
handler = MqttHandler:new(mqtt_ha, _registry)

-- 6. Połączenie i start
mqtt_ha:connect()
handler:start()

-- 7. Automatyczna synchronizacja stanów
for _, obj in pairs(_registry) do
    if obj.onChange then
        obj:onChange(function(newValue)
            handler:publishState(obj)
        end)
    end
end
```
