package homekit

import (
	"fmt"
	"log"
	"math"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
)

// readonlyPerms drops PairedWrite from a characteristic so the Home app renders the
// accessory as a read-only status tile instead of a control the user can (uselessly)
// toggle. Applied to the control characteristic of readonly accessories.
var readonlyPerms = []string{characteristic.PermissionRead, characteristic.PermissionEvents}

// command runs a write from the Home app on the exposed object at path:
// luaCall is Lua run with the handle h and hk, the operations of lua/command.lua.
// The HomeKit access level is read again here (the profile may have changed since the bridge was built); a
// refused or failed write puts the tile back to the real state.
func (s *Server) command(path, luaCall string, update func(float64)) bool {
	if s.paused(path, update) {
		return false
	}
	if s.accessManager != nil && (s.accessManager.IsHidden(accesscontrol.IntegrationHomeKit, path) ||
		s.accessManager.IsReadonly(accesscontrol.IntegrationHomeKit, path)) {
		log.Printf("[HomeKit] write to %s refused: read-only or hidden for HomeKit", path)
		s.revert(path, update)
		return false
	}
	if s.metrics != nil {
		s.metrics.RecordHomeKitRequest(false)
	}
	script := commandLua + "\n" + fmt.Sprintf("return command(ExposedObjects:get(%s), %s)", luaString(path), luaString(luaCall))
	res, err := s.executeCommand(script)
	if err == nil && res == "missing" {
		err = fmt.Errorf("object no longer exposed")
	}
	if err != nil {
		log.Printf("[HomeKit] Error executing command on %s: %v", path, err)
		s.revert(path, update)
		return false
	}
	return true
}

// paused reports a write that arrived while Home app writes are paused (Lua
// reload); the tile shows the real state again once Lua answers.
func (s *Server) paused(path string, update func(float64)) bool {
	if !s.IsInitializing() {
		return false
	}
	go s.revert(path, update)
	return true
}

// revert shows the object's real state again after a refused write.
func (s *Server) revert(path string, update func(float64)) {
	if update == nil {
		return
	}
	if v, ok := s.GetCurrentState(path); ok {
		update(v)
	}
}

// createExposedAccessory creates a HomeKit accessory for an ExposedObject
func (s *Server) createExposedAccessory(obj ExposedObject, readonly bool) *AccessoryWrapper {
	info := accessory.Info{
		Name:         obj.displayName(),
		SerialNumber: obj.Path,
		Manufacturer: "vCLU Exposed",
		Model:        obj.Type,
	}

	var acc *accessory.A
	var updateFunc func(float64)

	switch obj.Type {
	case "switch":
		acc, updateFunc = s.createExposedSwitch(info, obj, readonly)
	case "light", "dimmer":
		acc, updateFunc = s.createExposedLightbulb(info, obj, readonly)
	case "cover":
		acc, updateFunc = s.createExposedCover(info, obj, readonly)
	case "sensor", "motion":
		acc, updateFunc = s.createExposedMotionSensor(info, obj)
	case "temperature":
		acc, updateFunc = s.createExposedThermometer(info, obj)
	case "humidity":
		acc, updateFunc = s.createExposedHumiditySensor(info, obj)
	case "fan":
		acc, updateFunc = s.createExposedFan(info, obj, readonly)
	case "scene":
		acc, updateFunc = s.createExposedScene(info, obj, readonly)
	case "illuminance":
		acc, updateFunc = s.createExposedLightSensor(info, obj)
	case "binary_sensor":
		acc, updateFunc = s.createExposedContactSensor(info, obj)
	default:
		// number, button, lock, garage_door, climate...: a plain switch would
		// let the Home app (and "turn everything off") write 0/1 into them, and
		// a lock or gate as a switch skips the unlock HomeKit requires.
		log.Printf("[HomeKit] %s (%s) has no HomeKit accessory, left out", obj.Path, obj.Type)
		return nil
	}

	var wrapper *AccessoryWrapper
	if acc != nil {
		wrapper = &AccessoryWrapper{
			Accessory:  acc,
			Path:       obj.Path,
			Type:       obj.Type,
			Readonly:   readonly,
			UpdateFunc: updateFunc,
			Refetch:    obj.Type == "cover",
		}
	}

	return wrapper
}

// createExposedSwitch creates a Switch for an ExposedObject
func (s *Server) createExposedSwitch(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	sw := accessory.NewSwitch(info)

	// Get initial state
	if state, ok := obj.Value.(float64); ok {
		sw.Switch.On.SetValue(state > 0)
	} else if state, ok := obj.Value.(bool); ok {
		sw.Switch.On.SetValue(state)
	}

	if !readonly {
		sw.Switch.On.OnValueRemoteUpdate(func(on bool) {
			if s.paused(obj.Path, func(v float64) { sw.Switch.On.SetValue(v > 0) }) {
				return
			}
			currentState, ok := s.GetCurrentState(obj.Path)
			currentOn := currentState > 0
			if ok && currentOn == on {
				return
			}
			val := 0
			if on {
				val = 1
			}
			s.command(obj.Path, fmt.Sprintf("h:setValue(%d)", val), func(v float64) { sw.Switch.On.SetValue(v > 0) })
		})
	} else {
		sw.Switch.On.Permissions = readonlyPerms
	}

	updateFunc := func(value float64) {
		sw.Switch.On.SetValue(value > 0)
	}

	return sw.A, updateFunc
}

// createExposedLightbulb creates a Lightbulb. An on/off light (a DOUT exposed
// as "light") is switched with 1/0; a dimmable one (value 0..1) also gets a
// Brightness slider and is switched with its switchOn/switchOff, which keep
// the last brightness.
func (s *Server) createExposedLightbulb(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	light := accessory.NewLightbulb(info)
	on := light.Lightbulb.On

	var brightness *characteristic.Brightness
	if obj.Dimmable {
		brightness = characteristic.NewBrightness()
		light.Lightbulb.AddC(brightness.C)
	}

	updateFunc := func(value float64) {
		on.SetValue(value > 0)
		if brightness != nil && value > 0 {
			brightness.SetValue(int(math.Max(1, math.Min(100, math.Round(value*100)))))
		}
	}
	if state, ok := obj.Value.(float64); ok {
		updateFunc(state)
	}

	if readonly {
		on.Permissions = readonlyPerms
		if brightness != nil {
			brightness.Permissions = readonlyPerms
		}
		return light.A, updateFunc
	}

	on.OnValueRemoteUpdate(func(want bool) {
		if s.paused(obj.Path, updateFunc) {
			return
		}
		if current, ok := s.GetCurrentState(obj.Path); ok && (current > 0) == want {
			return // with a brightness change the Home app also sends On
		}
		call := "h:setValue(0)"
		switch {
		case want && obj.Dimmable:
			call = "hk.switchOn(h)"
		case want:
			call = "h:setValue(1)"
		case obj.Dimmable:
			call = "hk.switchOff(h)"
		}
		s.command(obj.Path, call, updateFunc)
	})
	if brightness != nil {
		brightness.OnValueRemoteUpdate(func(pct int) {
			if s.paused(obj.Path, updateFunc) {
				return
			}
			if s.command(obj.Path, fmt.Sprintf("hk.setBrightness(h, %d)", pct), updateFunc) {
				on.SetValue(pct > 0) // the slider also switches the light
			}
		})
	}

	return light.A, updateFunc
}

// createExposedCover creates a WindowCovering; vCLU and HomeKit both count 0 = closed.
// A Grenton roller without a position has its movement as the value, so the shown
// position is read with getPosition (Refetch).
func (s *Server) createExposedCover(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	cover := accessory.NewWindowCovering(info)
	wc := cover.WindowCovering

	toHomeKit := func(pos float64) int { return int(math.Max(0, math.Min(100, math.Round(pos)))) }
	updateFunc := func(pos float64) {
		wc.CurrentPosition.SetValue(toHomeKit(pos))
		wc.TargetPosition.SetValue(toHomeKit(pos))
	}
	if obj.Position != nil {
		updateFunc(*obj.Position)
	} else if state, ok := obj.Value.(float64); ok && !obj.HasPos {
		updateFunc(state) // position unknown (not reported yet): leave the default
	}

	if readonly {
		wc.TargetPosition.Permissions = readonlyPerms
		return cover.A, updateFunc
	}

	wc.TargetPosition.OnValueRemoteUpdate(func(target int) {
		if s.paused(obj.Path, updateFunc) {
			return
		}
		if current, ok := s.GetCurrentState(obj.Path); ok && int(math.Round(current)) == target {
			return
		}
		// a roller without a position opens or closes (hk.setPosition)
		if s.command(obj.Path, fmt.Sprintf("hk.setPosition(h, %d)", target), updateFunc) && obj.HasPos {
			// position never reported (Grenton roller): show the target, or
			// the Home app waits for the cover to arrive forever
			if _, ok := s.GetCurrentState(obj.Path); !ok {
				wc.CurrentPosition.SetValue(target)
			}
		}
	})

	return cover.A, updateFunc
}

// createExposedMotionSensor creates a MotionSensor for an ExposedObject
func (s *Server) createExposedMotionSensor(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	motion := accessory.NewMotionSensor(info)

	if state, ok := obj.Value.(float64); ok {
		motion.MotionSensor.MotionDetected.SetValue(state > 0)
	} else if state, ok := obj.Value.(bool); ok {
		motion.MotionSensor.MotionDetected.SetValue(state)
	}

	updateFunc := func(value float64) {
		motion.MotionSensor.MotionDetected.SetValue(value > 0)
	}

	return motion.A, updateFunc
}

// createExposedThermometer creates a TemperatureSensor for an ExposedObject
func (s *Server) createExposedThermometer(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	therm := accessory.NewTemperatureSensor(info)
	// hap defaults to 0..100 and clamps; outdoor sensors go below zero
	therm.TempSensor.CurrentTemperature.SetMinValue(-270)
	therm.TempSensor.CurrentTemperature.SetMaxValue(100)

	if state, ok := obj.Value.(float64); ok {
		therm.TempSensor.CurrentTemperature.SetValue(state)
	}

	updateFunc := func(value float64) {
		therm.TempSensor.CurrentTemperature.SetValue(value)
	}

	return therm.A, updateFunc
}

// createExposedHumiditySensor creates a proper HomeKit HumiditySensor accessory.
func (s *Server) createExposedHumiditySensor(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	a := accessory.New(info, accessory.TypeSensor)

	humiditySvc := service.NewHumiditySensor()
	a.AddS(humiditySvc.S)

	if state, ok := obj.Value.(float64); ok {
		humiditySvc.CurrentRelativeHumidity.SetValue(state)
	}

	updateFunc := func(value float64) {
		humiditySvc.CurrentRelativeHumidity.SetValue(value)
	}

	return a, updateFunc
}

// createExposedScene creates a stateless Switch for a scene
// The switch triggers the scene when turned ON and immediately returns to OFF
func (s *Server) createExposedScene(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	sw := accessory.NewSwitch(info)

	// Scenes always start OFF
	sw.Switch.On.SetValue(false)
	if readonly {
		sw.Switch.On.Permissions = readonlyPerms
		return sw.A, func(float64) {}
	}

	sw.Switch.On.OnValueRemoteUpdate(func(on bool) {
		if s.paused(obj.Path, func(float64) { sw.Switch.On.SetValue(false) }) {
			return
		}
		if on {
			s.command(obj.Path, "h:executeScene()", nil)
			// A scene tile always returns to OFF
			sw.Switch.On.SetValue(false)
		}
	})

	// Scene switch always shows OFF
	updateFunc := func(value float64) {
		sw.Switch.On.SetValue(false)
	}

	return sw.A, updateFunc
}

// createExposedFan creates a FanV2 with RotationSpeed for an ExposedObject.
// Speed levels (0-4) are mapped to percentages (0%, 25%, 50%, 75%, 100%).
func (s *Server) createExposedFan(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	a := accessory.New(info, accessory.TypeFan)

	fanSvc := service.NewFanV2()

	// Add RotationSpeed characteristic (0-100%)
	speed := characteristic.NewRotationSpeed()
	speed.SetMinValue(0)
	speed.SetMaxValue(100)
	speed.SetStepValue(25)
	fanSvc.AddC(speed.C)

	a.AddS(fanSvc.S)

	// Map level (0-4) → percentage (0-100)
	levelToPercent := func(level float64) float64 {
		return math.Min(100, math.Max(0, level*25))
	}
	// Map percentage (0-100) → level (0-4)
	percentToLevel := func(pct float64) int {
		return int(math.Round(math.Min(4, math.Max(0, pct/25))))
	}

	// Set initial state
	if state, ok := obj.Value.(float64); ok {
		fanSvc.Active.SetValue(int(math.Min(1, state)))
		speed.SetValue(levelToPercent(state))
	}

	fanUpdate := func(value float64) {
		if value > 0 {
			fanSvc.Active.SetValue(1)
		} else {
			fanSvc.Active.SetValue(0)
		}
		speed.SetValue(levelToPercent(value))
	}

	if !readonly {
		fanSvc.Active.OnValueRemoteUpdate(func(active int) {
			if s.paused(obj.Path, fanUpdate) {
				return
			}
			level := 0
			if active > 0 {
				level = 1 // turn on at level 1
			}
			if s.command(obj.Path, fmt.Sprintf("h:setValue(%d)", level), fanUpdate) {
				speed.SetValue(levelToPercent(float64(level)))
			}
		})

		speed.OnValueRemoteUpdate(func(pct float64) {
			if s.paused(obj.Path, fanUpdate) {
				return
			}
			level := percentToLevel(pct)
			if !s.command(obj.Path, fmt.Sprintf("h:setValue(%d)", level), fanUpdate) {
				return
			}
			// Update active state based on level
			if level == 0 {
				fanSvc.Active.SetValue(0)
			} else {
				fanSvc.Active.SetValue(1)
			}
		})
	} else {
		fanSvc.Active.Permissions = readonlyPerms
		speed.Permissions = readonlyPerms
	}

	return a, fanUpdate
}

// createExposedLightSensor creates a LightSensor (lux) for an ExposedObject.
func (s *Server) createExposedLightSensor(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	a := accessory.New(info, accessory.TypeSensor)
	svc := service.NewLightSensor()
	a.AddS(svc.S)

	// HAP range is 0.0001..100000 lux
	updateFunc := func(value float64) {
		svc.CurrentAmbientLightLevel.SetValue(math.Min(100000, math.Max(0.0001, value)))
	}
	if state, ok := obj.Value.(float64); ok {
		updateFunc(state)
	}
	return a, updateFunc
}

// createExposedContactSensor creates a ContactSensor: value 1 (active) is
// open, 0 is closed.
func (s *Server) createExposedContactSensor(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	a := accessory.New(info, accessory.TypeSensor)
	svc := service.NewContactSensor()
	a.AddS(svc.S)

	updateFunc := func(value float64) {
		if value > 0 {
			svc.ContactSensorState.SetValue(characteristic.ContactSensorStateContactNotDetected)
		} else {
			svc.ContactSensorState.SetValue(characteristic.ContactSensorStateContactDetected)
		}
	}
	if state, ok := obj.Value.(float64); ok {
		updateFunc(state)
	} else if state, ok := obj.Value.(bool); ok && state {
		updateFunc(1)
	}
	return a, updateFunc
}
