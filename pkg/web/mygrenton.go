package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// The myGrenton interfaces of the imported project (#219), checked by omproject,
// one <id>.json each, next to the om.lua files and not among the user's.
const myGrentonDir = "mygrenton"

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
	if err := s.removeInterfaces(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("failed to store the myGrenton interface: %v", err)
	}
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
func (s *Server) readInterfaces() ([]omproject.Interface, error) {
	files, err := filepath.Glob(filepath.Join(s.myGrentonPath(), "*.json"))
	if err != nil {
		return nil, err
	}
	list := []omproject.Interface{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var in omproject.Interface
		if err := json.Unmarshal(b, &in); err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Base(f), err)
		}
		list = append(list, in)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	return list, nil
}
