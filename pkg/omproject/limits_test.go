package omproject

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// cluXML is a CLU with its lists; ios goes inside iosList.
func cluXML(id, ios string) string {
	return fmt.Sprintf(`<specificObject class="CLU"><name>c%[1]s</name><nameOnCLU>%[1]s</nameOnCLU>`+
		`<iosList>%[2]s</iosList><modulesList/><firmwareType>00000003</firmwareType><hardwareType>00000013</hardwareType></specificObject>`, id, ios)
}

func systemXML(clus ...string) string {
	return "<object-stream><TreeObject><children>" + strings.Join(clus, "") + "</children></TreeObject></object-stream>"
}

// Many CLUs sharing one long list by reference read it once each: the reading stops at its
// budget instead of growing with their product.
func TestReadCapsVisits(t *testing.T) {
	pad := strings.Repeat("<x/>", 100)
	list := `<iosList id="1">` + strings.Repeat("<Input>"+pad+"</Input>", 2000) + `</iosList>`
	clus := []string{fmt.Sprintf(`<specificObject class="CLU"><nameOnCLU>CLU1</nameOnCLU>%s<modulesList/></specificObject>`, list)}
	for i := 2; i <= 200; i++ {
		clus = append(clus, fmt.Sprintf(`<specificObject class="CLU"><nameOnCLU>CLU%d</nameOnCLU><iosList reference="1"/><modulesList/></specificObject>`, i))
	}
	_, err := Read(projectZip(t, map[string]string{"system.xml": systemXML(clus...)}))
	if !errors.Is(err, errTooLarge) || !strings.Contains(err.Error(), "elements read") {
		t.Fatalf("err %v", err)
	}
}

// A project with more CLUs than any house is refused.
func TestReadCapsCLUs(t *testing.T) {
	var clus []string
	for i := 1; i <= maxCLUs+1; i++ {
		clus = append(clus, cluXML(fmt.Sprintf("CLU%d", i), ""))
	}
	if _, err := Read(projectZip(t, map[string]string{"system.xml": systemXML(clus...)})); !errors.Is(err, errTooLarge) {
		t.Fatalf("err %v", err)
	}
	if p, err := Read(projectZip(t, map[string]string{"system.xml": systemXML(clus[:maxCLUs]...)})); err != nil || len(p.CLUs) != maxCLUs {
		t.Fatalf("%d CLUs: %v", maxCLUs, err)
	}
}

// A CLU spelled out twice is imported once, with a warning.
func TestReadDedupesCLUs(t *testing.T) {
	sys := systemXML(cluXML("CLU1", ""), cluXML("CLU2", ""), cluXML("CLU1", ""))
	p, err := Read(projectZip(t, map[string]string{"system.xml": sys}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CLUs) != 2 || p.CLUs[0].ID != "CLU1" || p.CLUs[1].ID != "CLU2" {
		t.Errorf("CLUs %+v", p.CLUs)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "CLU1: the CLU is in the project twice") {
		t.Errorf("warnings %q", p.Warnings)
	}
}

// Warnings stop at MaxWarnings; the rest are counted and the docs say how many.
func TestReadCapsWarnings(t *testing.T) {
	ios := strings.Repeat("<Input><name>x</name></Input>", 300)
	p, err := Read(projectZip(t, map[string]string{"system.xml": systemXML(cluXML("CLU1", ios))}))
	if err != nil {
		t.Fatal(err)
	}
	// 300 objects without a name on the CLU, and no OM version
	if len(p.Warnings) != MaxWarnings || p.WarningsOmitted != 201 {
		t.Fatalf("%d warnings, %d omitted", len(p.Warnings), p.WarningsOmitted)
	}
	if !strings.Contains(p.Docs()["index.md"], "- ... and 201 more\n") {
		t.Error("index.md does not count the warnings left out")
	}
	got := CapWarnings(append(p.Warnings, "a", "b"), p.WarningsOmitted)
	if len(got) != MaxWarnings+1 || got[MaxWarnings] != "... and 203 more" {
		t.Errorf("capped: %d, last %q", len(got), got[len(got)-1])
	}
	if got := CapWarnings([]string{"a"}, 0); len(got) != 1 {
		t.Errorf("one warning: %q", got)
	}
}

// A CLU left out for its missing lists is in Skipped: an import must not take it for gone.
// One spelled out in full elsewhere is not.
func TestReadListsSkippedCLUs(t *testing.T) {
	broken := `<specificObject class="CLU"><nameOnCLU>CLU7</nameOnCLU><modulesList/></specificObject>`
	p, err := Read(projectZip(t, map[string]string{"system.xml": systemXML(cluXML("CLU1", ""), broken)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skipped) != 1 || p.Skipped[0] != "CLU7" {
		t.Errorf("skipped %q", p.Skipped)
	}
	p, err = Read(projectZip(t, map[string]string{"system.xml": systemXML(broken, cluXML("CLU7", ""))}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skipped) != 0 || len(p.CLUs) != 1 {
		t.Errorf("skipped %q, CLUs %d", p.Skipped, len(p.CLUs))
	}
}
