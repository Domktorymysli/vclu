//go:build race

package mcp

// raceEnabled loosens wall-clock bounds: the race detector slows code 5-10x.
const raceEnabled = true
