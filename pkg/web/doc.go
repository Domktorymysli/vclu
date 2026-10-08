// Package web implements the vCLU HTTP server: web UI pages and the REST API.
//
// The OpenAPI (Swagger 2.0) spec is generated from handler annotations:
//
//	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init \
//	  --dir pkg/web --generalInfo doc.go \
//	  --output web/api --outputTypes json \
//	  --parseDependency
//
// The generated web/api/swagger.json is served at /api/openapi.json and
// rendered at /api-docs (both behind authentication).
package web

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.4 init --dir . --generalInfo doc.go --output ../../web/api --outputTypes json --parseDependency

//	@title			vCLU HTTP API
//	@version		1.0
//	@description	REST API of the vCLU smart home controller (Grenton CLU emulator, MQTT/HomeKit/Satel integrations, Lua runtime).
//	@description
//	@description	Authentication: a session cookie (`vclu_session`, created via `POST /login`)
//	@description	or the `X-API-Key` header (key configured in vCLU settings). When authentication
//	@description	is not configured, all endpoints are open.
//
//	@BasePath	/
//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-Key
//
//	@tag.name			system
//	@tag.description	Health, restart, factory reset
//	@tag.name			satel
//	@tag.description	Satel Integra alarm integration
//	@tag.name			grenton
//	@tag.description	Grenton integration and remote CLU sync
//	@tag.name			mqtt
//	@tag.description	MQTT broker and client
//	@tag.name			homekit
//	@tag.description	Apple HomeKit bridge
//	@tag.name			registry
//	@tag.description	Object registry (devices exposed by the Lua runtime)
//	@tag.name			access
//	@tag.description	Access control profiles and assignments
//	@tag.name			backup
//	@tag.description	Configuration backup and restore
//	@tag.name			stats
//	@tag.description	Runtime statistics and metrics
//	@tag.name			settings
//	@tag.description	Device settings
