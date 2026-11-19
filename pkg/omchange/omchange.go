// Package omchange says what an Object Manager import changes: which CLUs and objects come,
// go or change type, and which places in the user's Lua name an object that is not there
// (any more). It reads parsed om.lua files and the modules directory; it writes nothing.
package omchange

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// CLU is one CLU as its om.lua describes it.
type CLU struct {
	ID        string
	Name      string
	Namespace string
	Objects   map[string]Object // by ID
	paths     map[string]bool
}

// Object is one object of a CLU.
type Object struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// House is the CLUs vclu knows, by ID.
type House map[string]*CLU

// Add puts a parsed om.lua in the house, in place of what it had for that CLU.
func (h House) Add(p *omparser.ParsedOM) {
	c := &CLU{ID: p.CLU.ID, Name: p.CLU.Name, Namespace: omparser.Namespace(p),
		Objects: map[string]Object{}, paths: map[string]bool{}}
	for _, o := range p.Objects {
		c.Objects[o.ID] = Object{ID: o.ID, Name: o.Name, Type: o.Class.String()}
	}
	for _, path := range omparser.Paths(p) {
		c.paths[path] = true
	}
	h[c.ID] = c
}

// Copy is a house to change without changing this one.
func (h House) Copy() House {
	out := House{}
	for id, c := range h {
		out[id] = c
	}
	return out
}

// Has says whether path ("CLU….DOU0001", "parter.light") names an object of the house.
func (h House) Has(path string) bool {
	for _, c := range h {
		if c.paths[path] {
			return true
		}
	}
	return false
}

// Change is what an import does to one CLU.
type Change struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"change"` // new, removed, changed, same
	Added   int       `json:"added"`
	Removed []Object  `json:"removed"`
	Retyped []Retyped `json:"retyped"`
	// Renamed is set when the CLU's name, and with it the table its objects are in, changes.
	Renamed string `json:"renamedFrom,omitempty"`
}

// Retyped is an object whose type changed.
type Retyped struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Diff says what turning before into after changes, by CLU ID.
func Diff(before, after House) []Change {
	ids := map[string]bool{}
	for id := range before {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	var out []Change
	for id := range ids {
		b, a := before[id], after[id]
		switch {
		case b == nil:
			out = append(out, Change{ID: id, Name: a.Name, Kind: "new", Added: len(a.Objects), Removed: []Object{}, Retyped: []Retyped{}})
		case a == nil:
			out = append(out, Change{ID: id, Name: b.Name, Kind: "removed", Removed: sorted(b.Objects), Retyped: []Retyped{}})
		default:
			ch := Change{ID: id, Name: a.Name, Kind: "same", Removed: []Object{}, Retyped: []Retyped{}}
			for oid, o := range a.Objects {
				if old, ok := b.Objects[oid]; !ok {
					ch.Added++
				} else if old.Type != o.Type {
					ch.Retyped = append(ch.Retyped, Retyped{ID: oid, Name: o.Name, From: old.Type, To: o.Type})
				}
			}
			gone := map[string]Object{}
			for oid, o := range b.Objects {
				if _, ok := a.Objects[oid]; !ok {
					gone[oid] = o
				}
			}
			ch.Removed = sorted(gone)
			sort.Slice(ch.Retyped, func(i, j int) bool { return ch.Retyped[i].ID < ch.Retyped[j].ID })
			if b.Namespace != a.Namespace {
				ch.Renamed = b.Name
			}
			if ch.Added > 0 || len(ch.Removed) > 0 || len(ch.Retyped) > 0 || ch.Renamed != "" {
				ch.Kind = "changed"
			}
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sorted(m map[string]Object) []Object {
	out := make([]Object, 0, len(m))
	for _, o := range m {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Ref is a place in the user's Lua that names an object.
type Ref struct {
	File string `json:"file"` // relative to the modules directory, with /
	Line int    `json:"line"`
	Path string `json:"path"`
}

var (
	// CLU221000002.DOU0001, or a name after the CLU, in code or in a string
	reCLUPath = regexp.MustCompile(`CLU[0-9]+\.[A-Za-z_][A-Za-z0-9_]*`)
	reTabPath = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*`)
	// names a file declares itself: locals, function parameters, loop variables
	reLocals = regexp.MustCompile(`\blocal\s+(?:function\s+)?([A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)` +
		`|\bfunction\b[^(\n]*\(([^)]*)\)` +
		`|\bfor\s+([A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)\s*(?:=|\bin\b)`)
	reName = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// declared are the names code declares as locals, parameters or loop variables anywhere in
// it: a CLU's table of the same name is not meant there (local light = ...; light.level).
func declared(code string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reLocals.FindAllStringSubmatch(code, -1) {
		for _, g := range m[1:] {
			for _, n := range reName.FindAllString(g, -1) {
				out[n] = true
			}
		}
	}
	return out
}

// Refs are the places in the .lua files under dir that name an object of a CLU, or of a CLU's
// table, of any of the houses: what an import can break. Comments are left out.
func Refs(dir string, houses ...House) ([]Ref, error) {
	clus, tables := map[string]bool{}, map[string]bool{}
	for _, h := range houses {
		for id, c := range h {
			clus[id] = true
			tables[c.Namespace] = true
		}
	}
	var out []Ref
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".lua") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		code := withoutComments(string(b))
		locals := declared(code)
		for i, line := range strings.Split(code, "\n") {
			seen := map[string]bool{}
			add := func(p string) {
				if !seen[p] {
					seen[p] = true
					out = append(out, Ref{File: rel, Line: i + 1, Path: p})
				}
			}
			for _, m := range reCLUPath.FindAllStringIndex(line, -1) {
				p := line[m[0]:m[1]]
				clu, member, _ := strings.Cut(p, ".")
				if clus[clu] && !strings.HasPrefix(member, "_") && !after(line, m[0]) {
					add(p)
				}
			}
			for _, m := range reTabPath.FindAllStringIndex(line, -1) {
				p := line[m[0]:m[1]]
				table, member, _ := strings.Cut(p, ".")
				if tables[table] && !locals[table] && !strings.HasPrefix(member, "_") && !after(line, m[0]) {
					add(p)
				}
			}
		}
		return nil
	})
	return out, err
}

// after says whether the match at i continues a name (x.parter.light, my_parter.light);
// after "..", the concatenation, it does not.
func after(line string, i int) bool {
	if i == 0 {
		return false
	}
	c := line[i-1]
	if c == '.' {
		return i < 2 || line[i-2] != '.'
	}
	return c == ':' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Missing are the refs whose object the house does not have.
func Missing(refs []Ref, h House) []Ref {
	out := []Ref{}
	for _, r := range refs {
		if !h.Has(r.Path) {
			out = append(out, r)
		}
	}
	return out
}

// withoutComments blanks Lua comments, keeping strings and every line where it was.
func withoutComments(code string) string {
	b := []byte(code)
	blank := func(from, to int) {
		for k := from; k < to && k < len(b); k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	// longBracket is the length of [==[ at i, 0 when there is none
	longBracket := func(i int) (int, string) {
		if i >= len(code) || code[i] != '[' {
			return 0, ""
		}
		j := i + 1
		for j < len(code) && code[j] == '=' {
			j++
		}
		if j < len(code) && code[j] == '[' {
			return j - i + 1, "]" + strings.Repeat("=", j-i-1) + "]"
		}
		return 0, ""
	}
	for i := 0; i < len(code); {
		switch c := code[i]; {
		case c == '-' && strings.HasPrefix(code[i:], "--"):
			if n, closing := longBracket(i + 2); n > 0 {
				end := strings.Index(code[i+2+n:], closing)
				stop := len(code)
				if end >= 0 {
					stop = i + 2 + n + end + len(closing)
				}
				blank(i, stop)
				i = stop
				continue
			}
			end := strings.IndexByte(code[i:], '\n')
			if end < 0 {
				end = len(code) - i
			}
			blank(i, i+end)
			i += end
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(code) && code[j] != c && code[j] != '\n' {
				if code[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
		case c == '[':
			if n, closing := longBracket(i); n > 0 {
				end := strings.Index(code[i+n:], closing)
				if end < 0 {
					return string(b)
				}
				i += n + end + len(closing)
				continue
			}
			i++
		default:
			i++
		}
	}
	return string(b)
}
