-- modules/satel/wiatrolap.lua (copy of the production module, 2026-10-07)
-- Nocą (21-6) ruch na PIR WIATROŁAP zapala światło na minutę; gasi je timer CLU.
-- Światła zapalonego przez kogoś innego nie ruszamy, a gdy ktoś inny je zgasi,
-- milkniemy na 10 min. "Kto" rozpoznajemy po aktorze zmiany (C2), nie po czasie.

local PIR   = _:get("satel.zone.2")          -- PIR WIATROŁAP
local LIGHT = _:get("CLU220000592.DOU4992")  -- Wiatrołap: Światło
local ON_S, PAUSE_S = 60, 10 * 60
local ME = "automation:wiatrolap"
-- Brak obiektu (np. Satel wyłączony) nie może przerwać ładowania reszty modułów
if not PIR or not LIGHT then return print("[WIATROLAP] Brak czujki albo światła, pomijam") end

-- Pauza przeżywa reload i restart (KV module:wiatrolap)
local state = persist("wiatrolap", { pauseUntil = 0 })

StateBus:watch("satel.zone.2", function()
    if not datetime.between("21:00", "06:00") or os.time() < state.pauseUntil then return end
    -- Zapalone przez kogoś innego (albo nie wiadomo przez kogo): nie przejmujemy
    local last = LIGHT:lastChange()
    if LIGHT:getValue() == 1 and not (last and last.actor == ME) then return end
    LIGHT:switchOn(ON_S * 1000)  -- ponowny ruch przedłuża
    Logger:getShared():info("[WIATROLAP] Ruch nocą, światło na %d s", ON_S)
end, { to = 1, runAs = ME })

-- Zgaszenie przez kogokolwiek poza nami (ściana, web, MCP, HA) to decyzja człowieka
StateBus:watch("CLU220000592.DOU4992", function(e)
    if e.actor == ME then return end
    state.pauseUntil = os.time() + PAUSE_S
    Logger:getShared():info("[WIATROLAP] Zgaszone przez %s, pauza %d min", e.actor, PAUSE_S / 60)
end, { from = 1, to = 0 })

print("[WIATROLAP] Aktywny: 21-6, " .. ON_S .. " s, pauza " .. PAUSE_S / 60 .. " min")
