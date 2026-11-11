package omparser

import (
	"strings"
	"testing"

	glua "github.com/yuin/gopher-lua"
)

// om.lua comes from an upload: a NAME_IO that is no identifier must not become code.
const namesOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
-- MODULES
mm_330000027 = OBJECT:new(2, 0x13ab669b, 0x1e, "13ab669b")
-- IO_MODULES
 DOU5048 = OBJECT:new(4, mm_330000027, 0)
-- NAME_IO a=1;os.exit(1);b=DOU5048
 DOU5049 = OBJECT:new(4, mm_330000027, 1)
-- NAME_IO back\";os.exit(1)--=DOU5049
 DOU5050 = OBJECT:new(4, mm_330000027, 2)
-- NAME_IO LAMP-1=DOU5050
`

func TestGenerateProxyNamesAreNoCode(t *testing.T) {
	parsed, err := Parse(namesOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	for name, out := range map[string]string{
		"proxy":       GenerateProxy(parsed, config),
		"local proxy": GenerateLocalProxy(parsed),
	} {
		if _, err := glua.NewState().LoadString(out); err != nil {
			t.Fatalf("%s does not compile: %v\n%s", name, err, out)
		}
	}
	// The names stay inside strings, the objects go by their IDs.
	out := GenerateProxy(parsed, config)
	for _, want := range []string{
		`dom.DOU5048 = RemoteSwitch:new(_clu, "DOU5048", "a=1;os.exit(1);b")`,
		`dom.DOU5049 = RemoteSwitch:new(_clu, "DOU5049", "back\\\";os.exit(1)--")`,
		`dom.LAMP_1 = RemoteSwitch:new(_clu, "DOU5050", "LAMP-1")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s\n%s", want, out)
		}
	}
}
