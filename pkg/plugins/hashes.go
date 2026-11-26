package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func sumMatches(want string, data []byte) bool {
	sum := sha256.Sum256(data)
	return want != "" && strings.EqualFold(want, hex.EncodeToString(sum[:]))
}

// ErrHashMismatch: the downloaded files are not the ones the repository index names.
var ErrHashMismatch = errors.New("the files differ from the repository index")

// checkHashes: each downloaded file has the digest the index gives it, and nothing the index
// names is missing. The official repository always gives them; another one may.
func checkHashes(dir string, listing PluginListing, repo config.PluginRepository) error {
	if len(listing.SHA256) == 0 {
		if IsOfficialRepo(repo) {
			return fmt.Errorf("%w: the official index gives no sha256", ErrHashMismatch)
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") { // the .complete marker; plugin files never start with a dot
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		want, ok := listing.SHA256[name]
		if !ok {
			return fmt.Errorf("%w: %s has no sha256", ErrHashMismatch, name)
		}
		if !sumMatches(want, data) {
			return fmt.Errorf("%w: %s", ErrHashMismatch, name)
		}
		seen[name] = true
	}
	for name := range listing.SHA256 {
		if !seen[name] {
			return fmt.Errorf("%w: %s not downloaded", ErrHashMismatch, name)
		}
	}
	return nil
}
