package dashspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

// Stored interfaces: one <id>.json each in a directory of their own (next to the om.lua
// files, not among the user's). The dashboards are generated from these alone.

// SaveInterfaces replaces the interfaces stored in dir with list; an empty list removes
// them. The new ones are written next to dir first, so a failed write leaves the old ones.
func SaveInterfaces(dir string, list []Interface) error {
	if len(list) == 0 {
		return RemoveInterfaces(dir)
	}
	fail := func(err error) error { return fmt.Errorf("failed to store the myGrenton interface: %v", err) }
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fail(err)
	}
	// what a write cut short left behind
	if left, _ := filepath.Glob(filepath.Join(parent, ".mygrenton-*")); len(left) > 0 {
		for _, l := range left {
			_ = os.RemoveAll(l)
		}
	}
	tmp, err := os.MkdirTemp(parent, ".mygrenton-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0755); err != nil {
		return fail(err)
	}
	for _, in := range list {
		if !ValidID(in.ID) {
			return fail(fmt.Errorf("interface id %q", in.ID))
		}
		b, err := json.MarshalIndent(in, "", "  ")
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, in.ID+".json"), b, 0644); err != nil {
			return fail(err)
		}
	}
	// the old ones step aside until the new ones are in place
	old := dir + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.Rename(old, dir)
		return fail(err)
	}
	_ = os.RemoveAll(old)
	return nil
}

// RemoveInterfaces removes the interfaces stored in dir.
func RemoveInterfaces(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to remove the old myGrenton interface: %v", err)
	}
	return nil
}

// LoadInterfaces reads the interfaces stored in dir, by label: only those and their pages
// whose IDs may name files. A file that does not read is logged and left out.
func LoadInterfaces(dir string) ([]Interface, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	list := []Interface{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var in Interface
		if err := json.Unmarshal(b, &in); err != nil {
			// one broken file does not hide the others
			log.Printf("[OM] %s: %v, left out", filepath.Base(f), err)
			continue
		}
		if !ValidID(in.ID) {
			continue
		}
		pages := in.Pages[:0]
		for _, p := range in.Pages {
			if ValidPageID(p.ID) {
				pages = append(pages, p)
			}
		}
		in.Pages = pages
		list = append(list, in)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	return list, nil
}
