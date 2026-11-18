package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/dashspec"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// The myGrenton interfaces of the imported project (#219) in vclu's own model
// (dashspec, translated by omproject), one <id>.json each, next to the om.lua
// files and not among the user's. The dashboards are generated from these alone.
const myGrentonDir = "panels"

func (s *Server) myGrentonPath() string {
	return filepath.Join(config.GetOMFilesDir(s.serverDir), myGrentonDir)
}

// writeInterfaces replaces the stored interfaces with the project's; a project
// without any removes them. Written next to the old ones first.
func (s *Server) writeInterfaces(p *omproject.Project) error {
	dir := s.myGrentonPath()
	if len(p.Interfaces) == 0 {
		return s.removeInterfaces()
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
	// what an import cut short left behind
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), ".mygrenton-*")); len(left) > 0 {
		for _, l := range left {
			_ = os.RemoveAll(l)
		}
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".mygrenton-")
	if err != nil {
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0755); err != nil {
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
	for _, in := range p.Interfaces {
		b, err := json.MarshalIndent(in, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to store the myGrenton interface: %v", err)
		}
		// the id passed omproject's check: letters, digits and -
		if err := os.WriteFile(filepath.Join(tmp, in.ID+".json"), b, 0644); err != nil {
			return fmt.Errorf("failed to store the myGrenton interface: %v", err)
		}
	}
	// the old ones step aside until the new ones are in place
	old := dir + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.Rename(old, dir)
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
	_ = os.RemoveAll(old)
	return nil
}

// removeInterfaces removes the stored interfaces.
func (s *Server) removeInterfaces() error {
	if err := os.RemoveAll(s.myGrentonPath()); err != nil {
		return fmt.Errorf("failed to remove the old myGrenton interface: %v", err)
	}
	return nil
}

// readInterfaces reads the stored interfaces, by label.
func (s *Server) readInterfaces() ([]dashspec.Interface, error) {
	files, err := filepath.Glob(filepath.Join(s.myGrentonPath(), "*.json"))
	if err != nil {
		return nil, err
	}
	list := []dashspec.Interface{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var in dashspec.Interface
		if err := json.Unmarshal(b, &in); err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Base(f), err)
		}
		list = append(list, in)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	return list, nil
}
