package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omchange"
	"github.com/Domktorymysli/vclu/pkg/omparser"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

var rePackOMLua = regexp.MustCompile(`^CLU(\d+)/om\.lua$`)

// house is what the stored om files describe.
func (s *Server) house() omchange.House {
	h := omchange.House{}
	dir := config.GetOMFilesDir(s.serverDir)
	for _, f := range config.GetOMFiles(s.serverDir) {
		b, err := os.ReadFile(filepath.Join(dir, f.Filename))
		if err != nil {
			continue
		}
		if p, err := omparser.Parse(string(b)); err == nil {
			h.Add(p)
		}
	}
	return h
}

// uploadHouse is what an upload would leave, without storing anything, as the upload takes
// the files (parseOMUpload): a project replaces the house but this vCLU's own file, a pack or
// an om.lua replaces its CLUs only. A project's removal is shown when every CLU it has would
// be imported; the upload also keeps them when storing one fails.
func (s *Server) uploadHouse(content []byte, filename string, before omchange.House) (omchange.House, bool, error) {
	own := s.ownSerial()
	isOwn := func(id string) bool { return own > 0 && extractSerialFromCLUID(id) == own }
	after := before.Copy()
	lower := strings.ToLower(filename)
	if !strings.HasSuffix(lower, ".omp") && !strings.HasSuffix(lower, ".zip") {
		_, _, p, err := parseOMUpload(content)
		if err != nil {
			return nil, false, err
		}
		after.Add(p)
		return after, false, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, false, fmt.Errorf("invalid ZIP file: %v", err)
	}
	if omproject.IsProject(zr) {
		project, err := omproject.ReadZip(zr)
		if err != nil {
			return nil, true, err
		}
		imported, failed := omchange.House{}, false
		for _, clu := range project.CLUs {
			if isOwn(clu.ID) {
				continue
			}
			// a CLU the upload could not take stays as it is
			if _, _, p, err := parseOMUpload([]byte(project.OMLua(clu))); err == nil {
				imported.Add(p)
			} else {
				failed = true
			}
		}
		if !failed && len(imported) > 0 {
			skipped := map[string]bool{}
			for _, id := range project.Skipped {
				skipped[id] = true
			}
			after = omchange.House{}
			for id, c := range before {
				if isOwn(id) || skipped[id] {
					after[id] = c
				}
			}
		}
		for id, c := range imported {
			after[id] = c
		}
		return after, true, nil
	}
	for _, f := range zr.File {
		m := rePackOMLua.FindStringSubmatch(f.Name)
		if m == nil || f.FileInfo().IsDir() {
			continue
		}
		if serial, _ := strconv.ParseUint(m[1], 10, 64); own > 0 && serial == own {
			continue
		}
		b, err := readZipFile(f)
		if err != nil {
			continue
		}
		if _, _, p, err := parseOMUpload(b); err == nil {
			after.Add(p)
		}
	}
	return after, false, nil
}

// brokenRefs are the places in modules/ that name an object the stored files do not have,
// among the names of before (the house before an import or a delete: a CLU renamed since
// takes its old table's name with it) and of what is stored now.
func (s *Server) brokenRefs(before omchange.House) []omchange.Ref {
	h := s.house()
	refs, err := omchange.Refs(filepath.Join(s.serverDir, modulesDirName), before, h)
	if err != nil {
		log.Printf("[OM] looking for objects named in modules/: %v", err)
	}
	return omchange.Missing(refs, h)
}

// handleAPIOMPreview says what uploading a file would change, storing nothing.
//
//	@Summary		Preview an OM upload
//	@Description	Takes the file the upload takes (.omp, .zip, .lua) and stores nothing. Answers per CLU what the upload changes (new, removed, changed, same: objects added, removed, of another type, the CLU's renamed table), the places in modules/ that work now and would name a missing object after it (breaks), and those that name a missing object already (broken).
//	@Tags			grenton
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file	true	"Object Manager project, diagnostic pack or om.lua"
//	@Success		200		{object}	map[string]interface{}	"project, clus, breaks, broken"
//	@Failure		400		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/om/preview [post]
func (s *Server) handleAPIOMPreview(c *gin.Context) {
	filename, content, ok := readOMUpload(c)
	if !ok {
		return
	}

	s.omImport.Lock()
	defer s.omImport.Unlock()
	before := s.house()
	after, project, err := s.uploadHouse(content, filename, before)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	refs, err := omchange.Refs(filepath.Join(s.serverDir, modulesDirName), before, after)
	if err != nil {
		log.Printf("[OM] looking for objects named in modules/: %v", err)
	}
	breaks, broken := []omchange.Ref{}, []omchange.Ref{}
	for _, r := range omchange.Missing(refs, after) {
		if before.Has(r.Path) {
			breaks = append(breaks, r)
		} else {
			broken = append(broken, r)
		}
	}
	changes := omchange.Diff(before, after)
	if changes == nil {
		changes = []omchange.Change{}
	}
	c.JSON(http.StatusOK, gin.H{"project": project, "clus": changes, "breaks": breaks, "broken": broken})
}
