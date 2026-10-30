package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sanitizePath converts a metric path to a safe directory name.
// "salda:outsideAir" → "salda.outsideAir"
func sanitizePath(metricPath string) string {
	s := strings.ReplaceAll(metricPath, ":", ".")
	s = strings.ReplaceAll(s, "/", ".")
	s = strings.ReplaceAll(s, "\\", ".")
	s = strings.ReplaceAll(s, "..", "")
	return s
}

// dailyFileName returns the filename for a given date.
func dailyFileName(t time.Time) string {
	return t.Format("2006-01-02") + ".ts"
}

// appendToFile appends samples to the daily .ts file for the metric.
func appendToFile(dataDir, metricPath string, samples []Sample) {
	if len(samples) == 0 {
		return
	}

	dirName := sanitizePath(metricPath)
	dir := filepath.Join(dataDir, dirName)

	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[METRICS] failed to create dir %s: %v", dir, err)
		return
	}

	// Group samples by day
	byDay := make(map[string][]Sample)
	for _, s := range samples {
		t := time.Unix(s.Timestamp, 0)
		day := dailyFileName(t)
		byDay[day] = append(byDay[day], s)
	}

	for day, daySamples := range byDay {
		filePath := filepath.Join(dir, day)
		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			log.Printf("[METRICS] failed to open %s: %v", filePath, err)
			continue
		}

		w := bufio.NewWriter(f)
		for _, s := range daySamples {
			// 10 digits: a kWh meter at 123456.78 keeps its hundredths for daily deltas
			fmt.Fprintf(w, "%d %.10g\n", s.Timestamp, s.Value)
		}
		w.Flush()
		f.Close()
	}
}

// readFiles reads daily .ts files for a metric, returning samples since the given timestamp.
func readFiles(dataDir, metricPath string, since int64) []Sample {
	dirName := sanitizePath(metricPath)
	dir := filepath.Join(dataDir, dirName)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	sinceTime := time.Unix(since, 0)
	// We need files from sinceTime's date and later
	sinceDate := sinceTime.Format("2006-01-02")

	var samples []Sample
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		// Skip files from before the since date
		fileDate := strings.TrimSuffix(e.Name(), ".ts")
		if fileDate < sinceDate {
			continue
		}

		fileSamples := readSingleFile(filepath.Join(dir, e.Name()), since)
		samples = append(samples, fileSamples...)
	}

	// Sort by timestamp
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp < samples[j].Timestamp
	})

	return samples
}

// readSingleFile parses a daily .ts file and returns samples at or after since.
// A last line without its newline is still being written and is left out.
func readSingleFile(path string, since int64) []Sample {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[:i]
	} else {
		return nil
	}

	var samples []Sample
	for _, line := range strings.Split(string(data), "\n") {
		// strconv, not Sscanf: a 30-day bars widget reads a month of lines
		tsStr, valStr, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil || ts < since {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
		if err != nil {
			continue
		}
		samples = append(samples, Sample{Timestamp: ts, Value: val})
	}
	return samples
}

// rotate removes daily files older than retainDays.
func rotate(dataDir string, retainDays int) {
	cutoff := time.Now().AddDate(0, 0, -retainDays).Format("2006-01-02")

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}

	for _, metricDir := range entries {
		if !metricDir.IsDir() {
			continue
		}
		dir := filepath.Join(dataDir, metricDir.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
				continue
			}
			fileDate := strings.TrimSuffix(f.Name(), ".ts")
			if fileDate < cutoff {
				os.Remove(filepath.Join(dir, f.Name()))
			}
		}
	}
}
