package lua

import (
	"testing"
	"time"
)

// Server.Stop cancels the loop and calls Close right away: Close must wait
// for RunLoop instead of closing the state under it, and closing drainDone
// from several paths must not panic.
func TestClose_WaitsForRunLoop(t *testing.T) {
	for i := 0; i < 20; i++ {
		engine := NewLuaEngine()
		stop := make(chan struct{})
		go engine.RunLoop(stop)
		for engineState(engine.engineState.Load()) != stateRunning {
			time.Sleep(time.Millisecond)
		}
		if _, err := engine.Execute(`_x = 1`); err != nil {
			t.Fatal(err)
		}
		close(stop)
		engine.Close()
		select {
		case <-engine.loopDone:
		default:
			t.Fatal("Close returned while RunLoop was still running")
		}
	}
}

func TestCloseDrain_Idempotent(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	engine.closeDrain()
	engine.closeDrain()
}
