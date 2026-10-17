package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// One JSONL file per UTC day: history/2026-10-06.jsonl. Files are written in
// batches (no fsync); a torn last line after a power cut is skipped on read.
const (
	dayLayout     = "2006-01-02"
	fileSuffix    = ".jsonl"
	maxRecordSize = 64 * 1024
	dirPerm       = 0700
	filePerm      = 0600
)

type diskStore struct {
	dir string
}

func dayOf(atMs int64) string {
	return time.UnixMilli(atMs).UTC().Format(dayLayout)
}

func (d *diskStore) path(day string) string {
	return filepath.Join(d.dir, day+fileSuffix)
}

// knownValue is the last value of an object in known.json: the baseline of
// a restart for objects whose last change is older than the memory window.
type knownValue struct {
	Value any   `json:"value"`
	AtMs  int64 `json:"atMs"`
}

const knownFile = "known.json"

// writeKnown replaces known.json whole (write, then rename).
func (d *diskStore) writeKnown(known map[string]knownValue) error {
	if err := os.MkdirAll(d.dir, dirPerm); err != nil {
		return err
	}
	data, err := json.Marshal(known)
	if err != nil {
		return err
	}
	tmp := filepath.Join(d.dir, knownFile+".tmp")
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(d.dir, knownFile))
}

// readKnown returns the snapshot, empty when there is none.
func (d *diskStore) readKnown() (map[string]knownValue, error) {
	known := map[string]knownValue{}
	data, err := os.ReadFile(filepath.Join(d.dir, knownFile))
	if os.IsNotExist(err) {
		return known, nil
	}
	if err != nil {
		return known, err
	}
	if err := json.Unmarshal(data, &known); err != nil {
		return map[string]knownValue{}, fmt.Errorf("%s: %w", knownFile, err)
	}
	return known, nil
}

// appendBatch writes entries to the file of their own UTC day (a batch that
// spans midnight goes to two files). Entries over 64 KiB are skipped and
// counted in skipped.
func (d *diskStore) appendBatch(entries []Entry) (skipped int, err error) {
	if err := os.MkdirAll(d.dir, dirPerm); err != nil {
		return 0, err
	}
	byDay := map[string][]byte{}
	var days []string
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil || len(line) > maxRecordSize {
			skipped++
			continue
		}
		day := dayOf(e.AtMs)
		if _, ok := byDay[day]; !ok {
			days = append(days, day)
		}
		byDay[day] = append(append(byDay[day], line...), '\n')
	}
	sort.Strings(days)
	for _, day := range days {
		data := byDay[day]
		// a write torn by a power cut must not swallow the first new record
		if !endsWithNewline(d.path(day)) {
			data = append([]byte{'\n'}, data...)
		}
		f, err := os.OpenFile(d.path(day), os.O_WRONLY|os.O_CREATE|os.O_APPEND, filePerm)
		if err != nil {
			return skipped, err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return skipped, werr
		}
		if cerr != nil {
			return skipped, cerr
		}
	}
	return skipped, nil
}

// endsWithNewline reports whether the file is missing, empty or ends with '\n'.
func endsWithNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return true
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, fi.Size()-1); err != nil {
		return true
	}
	return b[0] == '\n'
}

// days lists the day files, oldest first.
func (d *diskStore) days() []string {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*"+fileSuffix))
	var out []string
	for _, n := range names {
		day := strings.TrimSuffix(filepath.Base(n), fileSuffix)
		if _, err := time.Parse(dayLayout, day); err == nil {
			out = append(out, day)
		}
	}
	sort.Strings(out)
	return out
}

// scanDay calls fn for each entry of one day file, in file order. Lines that
// do not parse (a torn write, zeros after a crash) or are over the record
// limit are skipped without being held in memory; the rest is still read.
// keep, when set, cheaply rejects raw lines before they are decoded.
func (d *diskStore) scanDay(day string, keep func(line []byte) bool, fn func(Entry)) error {
	f, err := os.Open(d.path(day))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readLine(r, maxRecordSize+1024)
		if len(line) > 0 && (keep == nil || keep(line)) {
			var e Entry
			if json.Unmarshal(line, &e) == nil && e.ID != "" {
				fn(e)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine returns the next line; a line longer than max is consumed and
// returned empty, so a corrupt multi-megabyte line never sits in memory.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			return nil, err
		}
		return line, err
	}
}

// readDay returns the entries of one day file in file order (see scanDay).
func (d *diskStore) readDay(day string, keep func(line []byte) bool) ([]Entry, error) {
	var out []Entry
	err := d.scanDay(day, keep, func(e Entry) { out = append(out, e) })
	return out, err
}

// scanRange calls fn for entries with sinceMs <= atMs < untilMs (0 =
// unbounded), opening only the files of the days in the range.
func (d *diskStore) scanRange(sinceMs, untilMs int64, fn func(Entry)) error {
	for _, day := range d.days() {
		if sinceMs > 0 && day < dayOf(sinceMs) {
			continue
		}
		if untilMs > 0 && day > dayOf(untilMs) {
			break
		}
		err := d.scanDay(day, nil, func(e Entry) {
			if (sinceMs == 0 || e.AtMs >= sinceMs) && (untilMs == 0 || e.AtMs < untilMs) {
				fn(e)
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// usage returns the total size of the day files and the oldest day.
func (d *diskStore) usage() (bytes int64, oldest string) {
	for _, day := range d.days() {
		if fi, err := os.Stat(d.path(day)); err == nil {
			bytes += fi.Size()
			if oldest == "" {
				oldest = day
			}
		}
	}
	return bytes, oldest
}

// rotate deletes the oldest closed files while they are older than
// retentionDays or the total is over maxBytes. The file of today is never
// deleted, so a storm can exceed maxBytes until midnight.
func (d *diskStore) rotate(now time.Time, retentionDays int, maxBytes int64) (removed int, overLimit bool) {
	today := now.UTC().Format(dayLayout)
	cutoff := now.UTC().AddDate(0, 0, -retentionDays).Format(dayLayout)
	total, _ := d.usage()
	for _, day := range d.days() {
		if day >= today {
			break
		}
		if day >= cutoff && total <= maxBytes {
			break
		}
		fi, err := os.Stat(d.path(day))
		if err != nil {
			continue
		}
		if os.Remove(d.path(day)) == nil {
			total -= fi.Size()
			removed++
		}
	}
	return removed, total > maxBytes
}

// clear deletes all day files.
func (d *diskStore) clear() error {
	var firstErr error
	for _, day := range d.days() {
		if err := os.Remove(d.path(day)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", day, err)
		}
	}
	return firstErr
}
