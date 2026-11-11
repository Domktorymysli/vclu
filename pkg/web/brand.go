package web

import (
	"html/template"
	"strings"
)

// brandHTML escapes text and wraps each standalone word "vclu" in <span class="vclu">,
// which colours its first letter like the sidebar logo (dark-theme.css). Names such as
// vclu.pl, /opt/vclu, vclu-update or vclu_session stay plain text.
func brandHTML(text string) template.HTML {
	s := template.HTMLEscapeString(text)
	var b strings.Builder
	prev := byte(' ') // the byte before s; the start of the text joins nothing
	for {
		i := strings.Index(s, "vclu")
		if i < 0 {
			b.WriteString(s)
			break
		}
		if i > 0 {
			prev = s[i-1]
		}
		after := s[i+4:]
		word := !brandJoined(prev) && !brandContinues(after)
		b.WriteString(s[:i])
		if word {
			b.WriteString(`<span class="vclu">vclu</span>`)
		} else {
			b.WriteString("vclu")
		}
		s, prev = after, 'u'
	}
	return template.HTML(b.String())
}

// brandJoined reports whether c before "vclu" makes it part of a longer name.
func brandJoined(c byte) bool {
	return isWordByte(c) || strings.IndexByte("./:-", c) >= 0
}

// brandContinues reports whether the text after "vclu" makes it part of a longer name;
// a full stop or colon ending the sentence does not.
func brandContinues(after string) bool {
	if after == "" {
		return false
	}
	c := after[0]
	if isWordByte(c) || c == '/' || c == '-' {
		return true
	}
	return (c == '.' || c == ':') && len(after) > 1 && isWordByte(after[1])
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
