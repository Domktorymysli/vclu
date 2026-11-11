package omproject

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Limits on a parsed stream. The system.xml of a house with six CLUs is 3 MB:
// 61 thousand elements, nested 23 deep.
const (
	maxNodes = 1 << 20
	maxDepth = 128
)

// node is one element of an XStream object stream. XStream writes an object
// in full the first time it meets it (id="N") and as reference="N" after
// that, so a CLU can be spelled out inside a peripheral and only referenced
// from its own tree entry.
type node struct {
	name  string
	id    string
	ref   string
	class string
	text  string
	kids  []*node
}

// stream is a parsed object stream with its ids.
type stream struct {
	root     *node
	ids      map[string]*node
	dangling int // references to ids the stream never defines
}

// errTooLarge is the error of a stream over the limits.
var errTooLarge = errors.New("too large")

func parseStream(r io.Reader) (*stream, error) {
	d := xml.NewDecoder(r)
	s := &stream{ids: map[string]*node{}}
	var stack []*node
	var text strings.Builder
	nodes := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if nodes++; nodes > maxNodes {
				return nil, fmt.Errorf("%w: over %d elements", errTooLarge, maxNodes)
			}
			if len(stack) >= maxDepth {
				return nil, fmt.Errorf("%w: nested over %d deep", errTooLarge, maxDepth)
			}
			n := &node{name: t.Name.Local}
			for _, a := range t.Attr {
				switch a.Name.Local {
				case "id":
					n.id = a.Value
				case "reference":
					n.ref = a.Value
				case "class":
					n.class = a.Value
				}
			}
			if n.id != "" {
				s.ids[n.id] = n
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if s.root == nil {
				s.root = n
			}
			stack = append(stack, n)
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			n := stack[len(stack)-1]
			if len(n.kids) == 0 {
				n.text = strings.TrimSpace(text.String())
			}
			text.Reset()
			stack = stack[:len(stack)-1]
		}
	}
	if s.root == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return s, nil
}

// get follows a reference to the element it names; nil for a dangling one.
func (s *stream) get(n *node) *node {
	if n == nil || n.ref == "" {
		return n
	}
	if t := s.ids[n.ref]; t != nil {
		return t
	}
	s.dangling++
	return nil
}

// child is the first child element with the name, references followed.
func (s *stream) child(n *node, name string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.name == name {
			return s.get(k)
		}
	}
	return nil
}

// text is the text of a child element, "" when it is missing.
func (s *stream) text(n *node, name string) string {
	if c := s.child(n, name); c != nil {
		return c.text
	}
	return ""
}

// items are the elements of a list child, references followed.
func (s *stream) items(n *node, name string) []*node {
	l := s.child(n, name)
	if l == nil {
		return nil
	}
	out := make([]*node, 0, len(l.kids))
	for _, k := range l.kids {
		if t := s.get(k); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// walk calls fn for every element in document order.
func (n *node) walk(fn func(*node)) {
	fn(n)
	for _, k := range n.kids {
		k.walk(fn)
	}
}
