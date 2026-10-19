// Package lua provides embedded Lua code for the Grenton virtual CLU.
// All Lua modules are embedded from runtime/ directory.
package lua

import (
	_ "embed"
	"strings"
)

// Object class files (split for maintainability)
//
//go:embed runtime/objects/RemoteObject.lua
var remoteObject string

//go:embed runtime/objects/RemoteSwitch.lua
var remoteSwitch string

//go:embed runtime/objects/RemoteSensor.lua
var remoteSensor string

//go:embed runtime/objects/RemoteLight.lua
var remoteLight string

//go:embed runtime/objects/RemoteRGBLight.lua
var remoteRGBLight string

//go:embed runtime/objects/RemoteLedChannel.lua
var remoteLedChannel string

//go:embed runtime/objects/RemoteCover.lua
var remoteCover string

//go:embed runtime/objects/RemoteNumber.lua
var remoteNumber string

//go:embed runtime/objects/RemoteThermometer.lua
var remoteThermometer string

//go:embed runtime/objects/RemoteValueSensor.lua
var remoteValueSensor string

//go:embed runtime/objects/RemoteThermostat.lua
var remoteThermostat string

// Virtual object class files
//
//go:embed runtime/objects/RemotePanel.lua
var remotePanel string

//go:embed runtime/objects/RemotePanelButton.lua
var remotePanelButton string

//go:embed runtime/objects/VirtualObject.lua
var virtualObject string

//go:embed runtime/objects/VirtualSwitch.lua
var virtualSwitch string

//go:embed runtime/objects/VirtualDimmer.lua
var virtualDimmer string

//go:embed runtime/objects/VirtualCover.lua
var virtualCover string

//go:embed runtime/objects/VirtualSensor.lua
var virtualSensor string

//go:embed runtime/objects/VirtualNumber.lua
var virtualNumber string

//go:embed runtime/objects/VirtualText.lua
var virtualText string

// BaseClasses returns combined Lua code for all object classes.
// Combines: RemoteObject, RemoteSwitch, RemoteSensor, RemoteLight,
// RemoteRGBLight, RemoteLedChannel, RemoteCover, RemoteNumber, VirtualObject, VirtualSwitch,
// VirtualDimmer, VirtualCover, VirtualSensor, VirtualNumber, VirtualText.
func BaseClasses() string {
	var sb strings.Builder
	sb.WriteString("-- Base classes for remote Grenton objects\n\n")
	sb.WriteString(remoteObject)
	sb.WriteString("\n")
	sb.WriteString(remoteSwitch)
	sb.WriteString("\n")
	sb.WriteString(remoteSensor)
	sb.WriteString("\n")
	sb.WriteString(remoteLight)
	sb.WriteString("\n")
	sb.WriteString(remoteRGBLight)
	sb.WriteString("\n")
	sb.WriteString(remoteLedChannel)
	sb.WriteString("\n")
	sb.WriteString(remoteCover)
	sb.WriteString("\n")
	sb.WriteString(remoteNumber)
	sb.WriteString("\n")
	sb.WriteString(remoteThermometer)
	sb.WriteString("\n")
	sb.WriteString(remoteValueSensor)
	sb.WriteString("\n")
	sb.WriteString(remoteThermostat)
	sb.WriteString("\n")
	sb.WriteString(remotePanel)
	sb.WriteString("\n")
	sb.WriteString(remotePanelButton)
	sb.WriteString("\n\n-- Virtual object classes\n\n")
	sb.WriteString(virtualObject)
	sb.WriteString("\n")
	sb.WriteString(virtualSwitch)
	sb.WriteString("\n")
	sb.WriteString(virtualDimmer)
	sb.WriteString("\n")
	sb.WriteString(virtualCover)
	sb.WriteString("\n")
	sb.WriteString(virtualSensor)
	sb.WriteString("\n")
	sb.WriteString(virtualNumber)
	sb.WriteString("\n")
	sb.WriteString(virtualText)
	return sb.String()
}

// Registry files
//
//go:embed runtime/core/RemoteGroup.lua
var remoteGroup string

//go:embed runtime/core/Registry.lua
var registry string

//go:embed runtime/core/StateChange.lua
var stateChange string

// Registry returns combined Lua code for registry and RemoteGroup.
func Registry() string {
	var sb strings.Builder
	sb.WriteString("-- Object registry system\n\n")
	sb.WriteString(remoteGroup)
	sb.WriteString("\n")
	sb.WriteString(stateChange)
	sb.WriteString("\n")
	sb.WriteString(registry)
	return sb.String()
}

// Helpers contains utility functions: math helpers (clamp, lerp, map),
// scenes, sequences, timers (after, every), and logging.
//
//go:embed runtime/utils/Helpers.lua
var Helpers string
