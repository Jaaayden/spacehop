package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppInfo describes an installed or running program the user can bind.
type AppInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exe    string `json:"exe,omitempty"`
	Source string `json:"source"` // "start" | "running" | "store"
}

// LayoutInfo is an installed keyboard layout / input method.
type LayoutInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LogEntry is one line of the in-memory activity log.
type LogEntry struct {
	Time string `json:"time"`
	Text string `json:"text"`
	App  string `json:"app,omitempty"`
}

// UpdateInfo is what the settings window shows about updates.
type UpdateInfo struct {
	State   string // idle | checking | uptodate | available | downloading | error
	Latest  string // the newer version, e.g. "0.3.0"
	Page    string // its release page
	Err     string
	Percent int // download progress; -1 when unknown
}

// Incomplete reports whether the action lacks the field it needs to run.
func (a Action) Incomplete() bool {
	switch a.Type {
	case OpenApp, OpenFile, ExecuteFile:
		return a.Path == ""
	case OpenURL:
		return a.URL == ""
	case RunCommand:
		return a.Command == ""
	case Keystroke:
		return a.Chord == ""
	case InsertText:
		return a.Text == ""
	case MenuCommand:
		return a.Menu == ""
	case Window:
		return a.Window == ""
	case InputMethod:
		return a.Layout == ""
	case Notify:
		return a.Title == "" && a.Text == ""
	case System:
		return a.System == ""
	}
	return true
}

// Stats counts how often each binding ran. It only ever holds entries for
// bindings that exist, so it is bounded by MaxBindings.
type Stats struct {
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

func statsPath() string { return filepath.Join(Dir(), "stats.json") }

func LoadStats() *Stats {
	s := &Stats{Counts: map[string]int{}}
	if data, err := os.ReadFile(statsPath()); err == nil {
		_ = json.Unmarshal(data, s)
		if s.Counts == nil {
			s.Counts = map[string]int{}
		}
	}
	return s
}

// Save drops counts of bindings that no longer exist, then writes the file.
func (s *Stats) Save(c *Config) error {
	keep := make(map[string]int, len(s.Counts))
	for i := range c.Bindings {
		if n := s.Counts[c.Bindings[i].ID]; n > 0 {
			keep[c.Bindings[i].ID] = n
		}
	}
	s.Counts = keep
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := statsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statsPath())
}
