package updatecheck

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	codeRe = regexp.MustCompile("`([^`\n]+)`")
	linkRe = regexp.MustCompile(`\[([^\]\n]+)\]\((https://[^\s()\x00]+)\)`) // on escaped text
	boldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// placeholders for finished HTML; \x00 is removed from the input
	slotRe = regexp.MustCompile("\x00([0-9]+)\x00")
)

// RenderNotes turns release notes into safe HTML. Everything is escaped first; then only
// paragraphs, "- " lists, **bold**, `code` and [text](https://...) links opening in a new
// tab are made. Raw HTML and links other than https:// stay text.
func RenderNotes(md string) string {
	var b strings.Builder
	var para []string
	inList := false
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		item, isItem := strings.CutPrefix(line, "- ")
		if !isItem && inList {
			b.WriteString("</ul>")
			inList = false
		}
		switch {
		case line == "":
			flush()
		case isItem:
			flush()
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>" + inline(item) + "</li>")
		default:
			para = append(para, inline(line))
		}
	}
	flush()
	if inList {
		b.WriteString("</ul>")
	}
	return b.String()
}

// RenderInline renders one line (a summary's highlights or point) like RenderNotes, without blocks.
func RenderInline(md string) string {
	return inline(strings.Join(strings.Fields(md), " "))
}

// inline escapes a line, then renders code, links and bold; code is not reworked.
func inline(line string) string {
	s := html.EscapeString(strings.ReplaceAll(line, "\x00", ""))
	var slots []string
	slot := func(h string) string {
		slots = append(slots, h)
		return fmt.Sprintf("\x00%d\x00", len(slots)-1)
	}
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		return slot("<code>" + m[1:len(m)-1] + "</code>")
	})
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		p := linkRe.FindStringSubmatch(m)
		return slot(`<a href="` + p[2] + `" target="_blank" rel="noopener noreferrer">` + bold(p[1]) + "</a>")
	})
	s = bold(s)
	for range 2 { // a link's text may hold code
		s = slotRe.ReplaceAllStringFunc(s, func(m string) string {
			var i int
			fmt.Sscanf(slotRe.FindStringSubmatch(m)[1], "%d", &i)
			return slots[i]
		})
	}
	return s
}

func bold(s string) string {
	return boldRe.ReplaceAllString(s, "<strong>$1</strong>")
}
