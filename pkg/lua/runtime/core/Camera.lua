--- Camera — snapshot proxy for IP cameras.
-- Cameras register with the Go backend which handles auth (Digest/Basic)
-- and serves snapshots via /api/camera/:id/snapshot.
-- Dashboard image widgets point to that URL — no credentials in HTML.
--
-- Usage:
--   Camera.register("cam1", {
--       url = "http://192.168.0.203/ISAPI/Streaming/channels/102/picture",
--       auth = "digest",
--       username = "view",
--       password = "secret",
--   })
--
--   -- On dashboard:
--   :image("cam1", "Kamera", Camera.snapshotUrl("cam1"), {refresh=10, span=3})

local Camera = {}
Camera._registry = {}  -- local registry for sync when Go bridge arrives late

--- Register a camera for snapshot proxy.
-- @param id string Unique camera ID (used in URL)
-- @param opts table Camera options:
--   url      (string) Full URL to camera snapshot endpoint
--   auth     (string) Auth type: "digest", "basic", or nil for none
--   username (string) Username for auth
--   password (string) Password for auth
function Camera.register(id, opts)
    opts = opts or {}
    if not id or not opts.url then
        error("Camera.register: id and opts.url are required")
    end

    local auth = opts.auth or ""
    local username = opts.username or ""
    local password = opts.password or ""

    -- Store locally so _syncToGo can replay if bridge arrives later
    Camera._registry[id] = {url = opts.url, auth = auth, username = username, password = password}

    if __go_camera_register then
        __go_camera_register(id, opts.url, auth, username, password)
    else
        Logger:getShared():warn("[CAMERA] Go bridge not available yet, queued: " .. id)
    end
end

--- Re-register all cameras with Go bridge (called after SetCameraManager).
function Camera._syncToGo()
    if not __go_camera_register then return end
    for id, c in pairs(Camera._registry) do
        __go_camera_register(id, c.url, c.auth, c.username, c.password)
    end
end

--- Get the snapshot URL for a registered camera.
-- @param id string Camera ID
-- @return string URL path for image widget
function Camera.snapshotUrl(id)
    return "/api/camera/" .. id .. "/snapshot"
end

return Camera
