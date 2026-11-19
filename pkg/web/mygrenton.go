package web

import (
	"path/filepath"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/dashfiles"
	"github.com/Domktorymysli/vclu/pkg/dashspec"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// The myGrenton interfaces of the imported project (#219) in vclu's own model
// (dashspec, translated by omproject), stored next to the om.lua files and not
// among the user's; the dashboards made from them are in dashfiles.
const myGrentonDir = "panels"

func (s *Server) myGrentonPath() string {
	return filepath.Join(config.GetOMFilesDir(s.serverDir), myGrentonDir)
}

func (s *Server) dashFiles() dashfiles.Files {
	return dashfiles.New(filepath.Join(s.serverDir, modulesDirName))
}

// forgetCLU removes what a project import wrote about a CLU that is deleted. With last,
// the project's files and the myGrenton interfaces go too. The dashboards made from them
// are the user's and stay.
func (s *Server) forgetCLU(id string, last bool) error {
	if !last {
		return omproject.ForgetDocs(s.serverDir, id)
	}
	if err := omproject.RemoveDocs(s.serverDir); err != nil {
		return err
	}
	return dashspec.RemoveInterfaces(s.myGrentonPath())
}
