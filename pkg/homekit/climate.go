package homekit

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
)

// ExposedClimate is a thermostat exposed as climate (ExposedObjects._climateJSON).
type ExposedClimate struct {
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Step    float64  `json:"step"`
	Active  string   `json:"active"`  // heat or cool: the mode when on
	Mode    string   `json:"mode"`    // off, heat or cool; empty until reported
	Current *float64 `json:"current"` // temperature at its sensor
	Target  *float64 `json:"target"`
}

// The target range given to the Home app: HAP's 10-38 °C may be widened by the accessory;
// 5 °C is where common thermostats (frost protection) start.
const (
	homeKitTargetMin = 5.0
	homeKitTargetMax = 38.0
)

// targetRange keeps the expose min/max/step within what the Home app is given.
func (c ExposedClimate) targetRange() (lo, hi, step float64) {
	lo = math.Max(homeKitTargetMin, math.Min(homeKitTargetMax, c.Min))
	hi = math.Max(homeKitTargetMin, math.Min(homeKitTargetMax, c.Max))
	if !(lo < hi) {
		lo, hi = homeKitTargetMin, homeKitTargetMax
	}
	step = c.Step
	if !(step >= 0.1) {
		step = 0.1
	}
	return lo, hi, math.Min(step, hi-lo)
}

// heatingCooling is the HAP value of a mode: off 0, heat 1, cool 2.
func heatingCooling(mode string) int {
	switch mode {
	case "heat":
		return characteristic.TargetHeatingCoolingStateHeat
	case "cool":
		return characteristic.TargetHeatingCoolingStateCool
	}
	return characteristic.TargetHeatingCoolingStateOff
}

// createExposedThermostat creates a Thermostat. The mode is shown, not set, as in Home
// Assistant: off when stopped, else heat (or cool); a mode from the Home app is put back.
// Read-only: the target and the mode lose PairedWrite.
func (s *Server) createExposedThermostat(info accessory.Info, obj ExposedObject, readonly bool) (*accessory.A, func(float64), func(ExposedClimate)) {
	c := *obj.Climate
	th := accessory.NewThermostat(info)
	svc := th.Thermostat
	svc.CurrentTemperature.SetMinValue(-270) // as a thermometer: the sensor may be outdoors
	svc.CurrentTemperature.SetMaxValue(100)
	lo, hi, step := c.targetRange()
	svc.TargetTemperature.SetMinValue(lo)
	svc.TargetTemperature.SetMaxValue(hi)
	svc.TargetTemperature.SetStepValue(step)
	svc.TargetTemperature.SetValue(lo) // until reported; hap's 10 may be out of range
	modes := []int{characteristic.TargetHeatingCoolingStateOff, heatingCooling(c.Active)}
	svc.TargetHeatingCoolingState.ValidVals = modes
	svc.CurrentHeatingCoolingState.ValidVals = modes

	// both mode characteristics show the mode: neither thermostat reports its output
	show := func(st ExposedClimate) {
		if st.Current != nil {
			svc.CurrentTemperature.SetValue(*st.Current)
		}
		if st.Target != nil {
			svc.TargetTemperature.SetValue(*st.Target)
		}
		if st.Mode != "" {
			svc.TargetHeatingCoolingState.SetValue(heatingCooling(st.Mode))
			svc.CurrentHeatingCoolingState.SetValue(heatingCooling(st.Mode))
		}
	}
	if c.Mode == "" {
		c.Mode = c.Active // until reported, as exposed: on
	}
	show(c)
	update := func(v float64) { svc.CurrentTemperature.SetValue(v) }

	if readonly {
		svc.TargetTemperature.Permissions = readonlyPerms
		svc.TargetHeatingCoolingState.Permissions = readonlyPerms
		return th.A, update, show
	}

	// a refused or failed write shows the real state again
	revert := func() {
		if st, ok := s.currentClimate(obj.Path); ok {
			if st.Mode == "" {
				st.Mode = st.Active
			}
			show(st)
		}
	}
	svc.TargetTemperature.OnValueRemoteUpdate(func(v float64) {
		if s.paused(obj.Path, nil) {
			go revert()
			return
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || !s.command(obj.Path, "hk.setTarget(h, "+strconv.FormatFloat(v, 'f', -1, 64)+")", nil) {
			revert()
		}
	})
	svc.TargetHeatingCoolingState.OnValueRemoteUpdate(func(int) {
		log.Printf("[HomeKit] mode for %s not set: a thermostat's mode is shown, not set", obj.Path)
		if s.paused(obj.Path, nil) {
			go revert()
			return
		}
		revert()
	})
	return th.A, update, show
}

// currentClimate reads the thermostat at path; false when it is not one now.
func (s *Server) currentClimate(path string) (ExposedClimate, bool) {
	var st ExposedClimate
	res, err := s.luaExecutor.Execute(currentClimateLua + "\n" + fmt.Sprintf("return currentClimate(%s)", luaString(path)))
	if err != nil {
		log.Printf("[HomeKit] thermostat %s not read: %v", path, err)
		return st, false
	}
	if res == "nil" || json.Unmarshal([]byte(res), &st) != nil {
		return st, false
	}
	return st, true
}

// showClimate shows the current state on a thermostat accessory.
func (s *Server) showClimate(path string, w *AccessoryWrapper) {
	if w.ClimateFunc == nil {
		return
	}
	if st, ok := s.currentClimate(path); ok {
		w.ClimateFunc(st)
	}
}
