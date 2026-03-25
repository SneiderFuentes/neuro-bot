package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LogFilter defines filtering criteria for log queries.
type LogFilter struct {
	Lines  int       // Max lines to return (0 = all).
	Level  string    // Filter by level: debug, info, warn, error (empty = all).
	From   time.Time // Start time (zero = no lower bound).
	To     time.Time // End time (zero = no upper bound).
	Search string    // Substring search in "msg" field (empty = no filter).
}

// logEntry is the minimal JSON structure we need to parse for filtering.
type logEntry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// ReadLogs reads log files from dir matching prefix, applies filters, returns matching lines.
func ReadLogs(dir, prefix string, f LogFilter) ([]string, error) {
	files, err := findLogFiles(dir, prefix, f.From, f.To)
	if err != nil {
		return nil, err
	}

	var results []string
	levelUpper := strings.ToUpper(f.Level)

	for _, path := range files {
		lines, err := readAndFilter(path, levelUpper, f.From, f.To, f.Search)
		if err != nil {
			continue // skip unreadable files
		}
		results = append(results, lines...)
	}

	// Apply line limit (return last N lines).
	if f.Lines > 0 && len(results) > f.Lines {
		results = results[len(results)-f.Lines:]
	}

	return results, nil
}

// findLogFiles returns log file paths sorted chronologically that may contain entries in the time range.
func findLogFiles(dir, prefix string, from, to time.Time) ([]string, error) {
	pattern := filepath.Join(dir, fmt.Sprintf("%s-*.log", prefix))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob log files: %w", err)
	}
	if len(matches) == 0 {
		return nil, nil
	}

	sort.Strings(matches) // alphabetical = chronological with YYYY-MM-DD format

	// If we have a date range, filter files by filename date.
	if !from.IsZero() || !to.IsZero() {
		var filtered []string
		for _, path := range matches {
			fileDate := extractDate(path, prefix)
			if fileDate.IsZero() {
				filtered = append(filtered, path) // can't parse → include
				continue
			}
			// File date is the day; include if it overlaps with [from, to].
			fileEnd := fileDate.Add(24 * time.Hour)
			if !from.IsZero() && fileEnd.Before(from) {
				continue
			}
			if !to.IsZero() && fileDate.After(to) {
				continue
			}
			filtered = append(filtered, path)
		}
		matches = filtered
	}

	return matches, nil
}

// extractDate parses the date from a filename like "neuro-bot-2026-03-24.log".
func extractDate(path, prefix string) time.Time {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, prefix+"-")
	base = strings.TrimSuffix(base, ".log")
	t, err := time.Parse("2006-01-02", base)
	if err != nil {
		return time.Time{}
	}
	return t
}

// readAndFilter reads a single log file and returns lines matching the filter criteria.
func readAndFilter(path, levelUpper string, from, to time.Time, search string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024) // handle long lines

	searchLower := strings.ToLower(search)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Quick level check without full JSON parse when possible.
		if levelUpper != "" && !strings.Contains(line, `"level":"`+levelUpper) {
			continue
		}

		// Parse JSON for time and search filtering.
		if !from.IsZero() || !to.IsZero() || search != "" {
			var entry logEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}

			if !from.IsZero() || !to.IsZero() {
				t, err := time.Parse(time.RFC3339Nano, entry.Time)
				if err != nil {
					continue
				}
				if !from.IsZero() && t.Before(from) {
					continue
				}
				if !to.IsZero() && t.After(to) {
					continue
				}
			}

			if search != "" && !strings.Contains(strings.ToLower(entry.Msg), searchLower) {
				continue
			}
		}

		results = append(results, line)
	}

	return results, scanner.Err()
}
