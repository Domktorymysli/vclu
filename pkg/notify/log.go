package notify

import (
	"regexp"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/logstream"
)

// logDenied are sources whose errors reach the bell another way, or come from it.
var logDenied = map[string]bool{"AUTH": true, "NOTIFY": true, "LUA": true}

var (
	goFileRe   = regexp.MustCompile(`^\S+\.go:\d+:\s+`)
	levelTagRe = regexp.MustCompile(`^\[(?i:error)\]\s*`)
	sourceRe   = regexp.MustCompile(`^\[([^\]]+)\]\s*`)
	digitsRe   = regexp.MustCompile(`\d+`)
)

// FromLog is the entry for a log line tagged [ERROR]; false for any other line.
func FromLog(le logstream.LogEntry) (Entry, bool) {
	if le.TagLevel != LevelError {
		return Entry{}, false
	}
	msg := goFileRe.ReplaceAllString(strings.TrimSpace(le.Message), "")
	msg = levelTagRe.ReplaceAllString(msg, "")
	source := ""
	if m := sourceRe.FindStringSubmatch(msg); m != nil {
		source = m[1]
		msg = strings.TrimSpace(msg[len(m[0]):])
	} else if !strings.EqualFold(le.Source, "error") {
		source = le.Source
	}
	if logDenied[strings.ToUpper(source)] || msg == "" {
		return Entry{}, false
	}
	return Entry{
		Kind:   KindEvent,
		Source: SourceLog,
		Level:  LevelError,
		Key:    "log:" + source + ":" + clip(digitsRe.ReplaceAllString(msg, "#")),
		Msg:    MsgLog,
		Params: map[string]string{"source": source},
		Body:   msg,
		Link:   "/debug",
	}, true
}

// LogHook is a logstream hook adding tagged errors to s; lines logged while skip() is true
// (a Notifications.on handler running) are left out.
func LogHook(s *Store, skip func() bool) func(logstream.LogEntry) {
	return func(le logstream.LogEntry) {
		if skip != nil && skip() {
			return
		}
		if e, ok := FromLog(le); ok {
			s.Add(e)
		}
	}
}
