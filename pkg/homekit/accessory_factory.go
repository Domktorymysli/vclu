package homekit

import (
	"fmt"
	"log"
	"math"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

// readonlyPerms drops PairedWrite from a characteristic so the Home app renders the
// accessory as a read-only status tile instead of a control the user can (uselessly)
// toggle. Applied to the control characteristic of readonly accessories.
var readonlyPerms = []string{characteristic.PermissionRead, characteristic.PermissionEvents}

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
		acc, updateFunc = s.createExposedScene(info, obj)
	case "number":
		// Numbers don't have a good HomeKit equivalent, use switch
		acc, updateFunc = s.createExposedSwitch(info, obj, readonly)
	default:
		log.Printf("[HomeKit] unknown type %s for %s, using switch", obj.Type, obj.Path)
		acc, updateFunc = s.createExposedSwitch(info, obj, readonly)
	}

	var wrapper *AccessoryWrapper
	if acc != nil {
		wrapper = &AccessoryWrapper{
			Accessory:  acc,
			Path:       obj.Path,
			Type:       obj.Type,
			Readonly:   readonly,
			UpdateFunc: updateFunc,
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
			if s.IsInitializing() {
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
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:setValue(%d) end`, obj.Path, val)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error executing exposed command: %v", err)
			}
		})
	} else {
		sw.Switch.On.Permissions = readonlyPerms
	}

	updateFunc := func(value float64) {
		sw.Switch.On.SetValue(value > 0)
	}

	return sw.A, updateFunc
}

// createExposedLightbulb creates a Lightbulb for an ExposedObject
func (s *Server) createExposedLightbulb(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	light := accessory.NewLightbulb(info)

	// Get initial state
	if state, ok := obj.Value.(float64); ok {
		light.Lightbulb.On.SetValue(state > 0)
	}

	if !readonly {
		light.Lightbulb.On.OnValueRemoteUpdate(func(on bool) {
			if s.IsInitializing() {
				return
			}
			currentState, ok := s.GetCurrentState(obj.Path)
			currentOn := currentState > 0
			if ok && currentOn == on {
				return
			}
			val := 0
			if on {
				val = 100
			}
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:setValue(%d) end`, obj.Path, val)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error executing exposed command: %v", err)
			}
		})
	} else {
		light.Lightbulb.On.Permissions = readonlyPerms
	}

	updateFunc := func(value float64) {
		light.Lightbulb.On.SetValue(value > 0)
	}

	return light.A, updateFunc
}

// createExposedCover creates a WindowCovering for an ExposedObject
func (s *Server) createExposedCover(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64)) {
	cover := accessory.NewWindowCovering(info)

	// Get initial position
	if state, ok := obj.Value.(float64); ok {
		cover.WindowCovering.CurrentPosition.SetValue(int(state))
		cover.WindowCovering.TargetPosition.SetValue(int(state))
	}

	if !readonly {
		cover.WindowCovering.TargetPosition.OnValueRemoteUpdate(func(position int) {
			if s.IsInitializing() {
				return
			}
			currentState, ok := s.GetCurrentState(obj.Path)
			if ok && int(currentState) == position {
				return
			}
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:setValue(%d) end`, obj.Path, position)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error executing exposed command: %v", err)
			}
		})
	} else {
		cover.WindowCovering.TargetPosition.Permissions = readonlyPerms
	}

	updateFunc := func(value float64) {
		cover.WindowCovering.CurrentPosition.SetValue(int(value))
		cover.WindowCovering.TargetPosition.SetValue(int(value))
	}

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
func (s *Server) createExposedScene(info accessory.Info, obj ExposedObject) (*accessory.A, func(float64)) {
	sw := accessory.NewSwitch(info)

	// Scenes always start OFF
	sw.Switch.On.SetValue(false)

	sw.Switch.On.OnValueRemoteUpdate(func(on bool) {
		if s.IsInitializing() {
			return
		}
		if on {
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:executeScene() end`, obj.Path)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error executing scene: %v", err)
			}
			// Reset switch to OFF after a short delay
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

	if !readonly {
		fanSvc.Active.OnValueRemoteUpdate(func(active int) {
			if s.IsInitializing() {
				return
			}
			level := 0
			if active > 0 {
				level = 1 // turn on at level 1
			}
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:setValue(%d) end`, obj.Path, level)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error setting fan active: %v", err)
			}
			speed.SetValue(levelToPercent(float64(level)))
		})

		speed.OnValueRemoteUpdate(func(pct float64) {
			if s.IsInitializing() {
				return
			}
			level := percentToLevel(pct)
			script := fmt.Sprintf(`local h = ExposedObjects:get("%s"); if h then h:setValue(%d) end`, obj.Path, level)
			if s.metrics != nil {
				s.metrics.RecordHomeKitRequest(false)
			}
			if _, err := s.executeCommand(script); err != nil {
				log.Printf("[HomeKit] Error setting fan speed: %v", err)
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

	updateFunc := func(value float64) {
		if value > 0 {
			fanSvc.Active.SetValue(1)
		} else {
			fanSvc.Active.SetValue(0)
		}
		speed.SetValue(levelToPercent(value))
	}

	return a, updateFunc
}
