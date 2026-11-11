package logstream

import (
	"bytes"
	"io"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestBytesLastHour_CountsWhatReachesStdout(t *testing.T) {
	var out bytes.Buffer
	c := &fakeClock{t: time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)}
	ls := NewLogStream(&out, 10)
	ls.written.now = c.now

	_, _ = ls.Write([]byte("2026/11/09 20:00:00 [DEBUG] MQTT publish\n"))
	c.t = c.t.Add(30 * time.Minute)
	_, _ = ls.Write([]byte("2026/11/09 20:30:00 [INFO] started\n"))
	if got := ls.BytesLastHour(); got != int64(out.Len()) {
		t.Fatalf("counted %d, stdout got %d", got, out.Len())
	}

	c.t = c.t.Add(31 * time.Minute) // the first line is now over an hour old
	if got, want := ls.BytesLastHour(), int64(len("2026/11/09 20:30:00 [INFO] started\n")); got != want {
		t.Fatalf("after an hour: %d, want %d", got, want)
	}
	c.t = c.t.Add(2 * time.Hour)
	if got := ls.BytesLastHour(); got != 0 {
		t.Fatalf("idle: %d", got)
	}
}

// failingWriter writes nothing: a line that does not reach stdout is not counted.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBytesLastHour_NothingWrittenNothingCounted(t *testing.T) {
	ls := NewLogStream(failingWriter{}, 10)
	_, _ = ls.Write([]byte("2026/11/09 20:00:00 [DEBUG] dropped\n"))
	if got := ls.BytesLastHour(); got != 0 {
		t.Fatalf("counted %d", got)
	}
	if ls.EntryCount() != 1 {
		t.Fatal("the line must still be in the buffer")
	}
}

func TestBytesLastHour_LevelFilteredNotCounted(t *testing.T) {
	var out bytes.Buffer
	ls := NewLogStream(&out, 10)
	_, _ = ls.Write([]byte("2026/11/09 20:00:00 [DEBUG] MQTT publish\n"))
	if got := ls.BytesLastHour(); got != 0 || out.Len() != 0 {
		t.Fatalf("counted %d, stdout %q", got, out.String())
	}
	if ls.EntryCount() != 1 {
		t.Fatal("the line must still be in the buffer")
	}

	_ = ls.SetLevel("debug")
	line := "2026/11/09 20:00:01 [DEBUG] MQTT publish\n"
	_, _ = ls.Write([]byte(line))
	if got := ls.BytesLastHour(); got != int64(len(line)) {
		t.Fatalf("at debug: counted %d, want %d", got, len(line))
	}
}
