-- LDoc configuration for Virtual CLU Lua API
-- Run: ldoc .

project = "Virtual CLU"
title = "Lua API Documentation"
description = "API documentation for Virtual CLU Lua modules"

-- Output directory
dir = "../../../docs/lua-api"

-- Source - scan current directory
file = "."

-- Be lenient with non-standard doc comments
not_luadoc = true

-- Include all functions, even without docs
all = true

-- Sort alphabetically
sort = true

-- Use Markdown in descriptions
format = "markdown"

-- Module sorting
sort_modules = true

-- Custom tags
custom_tags = {
    { "usage", title = "Usage", hidden = false },
    { "compat", title = "OM Compatible", hidden = false },
    { "inherits", title = "Inherits", hidden = false },
    { "api", title = "API", hidden = false },
}
