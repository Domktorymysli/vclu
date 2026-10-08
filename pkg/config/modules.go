package config

import (
	"crypto/rand"
	"fmt"
)

type ModuleCategory string

const (
	ModuleCategoryOutput ModuleCategory = "output"
	ModuleCategoryInput  ModuleCategory = "input"
	ModuleCategoryAnalog ModuleCategory = "analog"
)

type IOType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	LuaType     int    `json:"luaType"`
	Prefix      string `json:"prefix"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

type ModuleType struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Category     ModuleCategory `json:"category"`
	Description  string         `json:"description"`
	HardwareType uint8          `json:"hardwareType"`
	SerialPrefix uint32         `json:"serialPrefix"`
	IOTypes      []IOType       `json:"ioTypes"`
	Icon         string         `json:"icon"`
}

type ModuleInstance struct {
	ID           string `json:"id"`
	TypeID       string `json:"typeId"`
	Serial       string `json:"serial"`
	Name         string `json:"name"`
	HardwareType uint8  `json:"hardwareType"`
}

type IOInstance struct {
	ID       string `json:"id"`
	ModuleID string `json:"moduleId"`
	TypeID   string `json:"typeId"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	LuaType  int    `json:"luaType"`
}

var PredefinedModules = []ModuleType{
	{
		ID:           "dout8t",
		Name:         "DOUT8T",
		Category:     ModuleCategoryOutput,
		Description:  "8x Digital Output + 8x Digital Input + 1x Analog",
		HardwareType: 0x1e,
		SerialPrefix: 3300,
		IOTypes: []IOType{
			{ID: "dout", Name: "DOUT", LuaType: 4, Prefix: "DOU", Description: "Digital Output", Count: 8},
			{ID: "din", Name: "DIN", LuaType: 3, Prefix: "DIN", Description: "Digital Input", Count: 8},
			{ID: "analogin", Name: "AnalogIN", LuaType: 12, Prefix: "ANA", Description: "Analog Input", Count: 1},
		},
		Icon: "bi-toggle-on",
	},
	{
		ID:           "relay4",
		Name:         "RELAY4",
		Category:     ModuleCategoryOutput,
		Description:  "4x Relay Output + 1x Analog",
		HardwareType: 0x15,
		SerialPrefix: 2010,
		IOTypes: []IOType{
			{ID: "relay_dout", Name: "DOUT", LuaType: 4, Prefix: "DOU", Description: "Relay Output", Count: 4},
			{ID: "analogin", Name: "AnalogIN", LuaType: 12, Prefix: "ANA", Description: "Analog Input", Count: 1},
		},
		Icon: "bi-lightning",
	},
	{
		ID:           "din8",
		Name:         "DIN8",
		Category:     ModuleCategoryInput,
		Description:  "8x Digital Input + 1x Analog",
		HardwareType: 0x14,
		SerialPrefix: 1810,
		IOTypes: []IOType{
			{ID: "din", Name: "DIN", LuaType: 3, Prefix: "DIN", Description: "Digital Input", Count: 8},
			{ID: "analogin", Name: "AnalogIN", LuaType: 12, Prefix: "ANA", Description: "Analog Input", Count: 1},
		},
		Icon: "bi-hand-index",
	},
}

func GetModuleByID(id string) *ModuleType {
	for i := range PredefinedModules {
		if PredefinedModules[i].ID == id {
			return &PredefinedModules[i]
		}
	}
	return nil
}

func GenerateModuleSerial(moduleType *ModuleType) string {
	randomPart := generateRandom5Digits()
	prefix := moduleType.SerialPrefix / 100
	return fmt.Sprintf("%d%07d", prefix, randomPart)
}

func GenerateIOID(prefix string) string {
	randomPart := generateRandom4Digits()
	return fmt.Sprintf("%s%04d", prefix, randomPart)
}

func GenerateIOObjectsForModule(moduleInstance *ModuleInstance, moduleType *ModuleType) []IOInstance {
	var objects []IOInstance
	for _, ioType := range moduleType.IOTypes {
		for i := 0; i < ioType.Count; i++ {
			objects = append(objects, IOInstance{
				ID:       GenerateIOID(ioType.Prefix),
				ModuleID: moduleInstance.ID,
				TypeID:   ioType.ID,
				Index:    i,
				Name:     fmt.Sprintf("%s_%s%d", moduleInstance.Name, ioType.Prefix, i+1),
				LuaType:  ioType.LuaType,
			})
		}
	}
	return objects
}

func generateRandom5Digits() uint32 {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 10000000
}

func generateRandom4Digits() uint16 {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return (uint16(b[0])<<8 | uint16(b[1])) % 10000
}
