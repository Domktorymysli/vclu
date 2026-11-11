package server

import (
	"bytes"
	"log"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// A report packet from a CLU writes nothing to stdout at the default level; the ring buffer keeps it.
func TestClientReportNotOnStdoutByDefault(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		_ev = {}
		StateBus:getShared():on("state_changed", function(e) table.insert(_ev, e) end)
		light = RemoteSwitch:new({ address = "127.0.0.1", executeNoReply = function() end }, "DOU1")
		registerObject("CLU9.DOU1", light)
		RemoteSubscription.activeSubscriptions["127.0.0.1:16"] = { cluSerial = "CLU9", objects = { { objId = "DOU1", attrId = 0 } } }
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{localIP: net.IPv4(127, 0, 0, 1), luaEngine: engine}

	var stdout bytes.Buffer
	ls := logstream.NewLogStream(&stdout, 100)
	log.SetOutput(ls)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s.handleClientReportMessage("127.0.0.1", "resp:127.0.0.1:00000000:clientReport:16:{1}")

	if stdout.Len() != 0 {
		t.Errorf("report packet on stdout:\n%s", stdout.String())
	}
	found := false
	for _, e := range ls.GetHistory() {
		if strings.Contains(e.Message, "[CLIENT-REPORT] from 127.0.0.1") {
			found = true
		}
	}
	if !found {
		t.Errorf("report line not in the ring buffer: %+v", ls.GetHistory())
	}
	if got := stateChanges(t, engine); !strings.HasPrefix(got, "CLU9.DOU1 ") {
		t.Errorf("report not applied: %q", got)
	}
}
