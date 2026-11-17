package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

var errNoFile = errors.New("not found")

// MaxIndexSize bounds a repository's plugins.json (and a root plugin.json).
const MaxIndexSize = 2 << 20

// FetchIndex reads a repository's plugins.json. A repository without it and with a
// plugin.json at its root is one plugin, listed as an index of one.
func FetchIndex(client *http.Client, repo config.PluginRepository) (*RepositoryIndex, error) {
	return FetchIndexCtx(context.Background(), client, repo)
}

// FetchIndexCtx is FetchIndex stopped by ctx.
func FetchIndexCtx(ctx context.Context, client *http.Client, repo config.PluginRepository) (*RepositoryIndex, error) {
	resolver := DefaultResolver(repo.URL)
	body, err := fetchRepoFile(ctx, client, resolver.RawFileURL(repo.URL, "", "plugins.json"))
	if errors.Is(err, errNoFile) {
		manifestBody, merr := fetchRepoFile(ctx, client, resolver.RawFileURL(repo.URL, "", "plugin.json"))
		if merr == nil {
			return singlePluginIndex(repo.URL, manifestBody)
		}
		if !errors.Is(merr, errNoFile) {
			return nil, merr
		}
		return nil, fmt.Errorf("no plugins.json or plugin.json in the repository")
	}
	if err != nil {
		return nil, err
	}
	var index RepositoryIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &index, nil
}

func fetchRepoFile(ctx context.Context, client *http.Client, fileURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repository: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repository: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoFile
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxIndexSize+1))
	if err == nil && len(body) > MaxIndexSize {
		err = fmt.Errorf("larger than %d bytes", MaxIndexSize)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return body, nil
}

// singlePluginIndex: the namespace is the manifest's ("@jan/foo") or else the
// repository owner's (github.com/Jan/foo -> "jan").
func singlePluginIndex(repoURL string, manifestBody []byte) (*RepositoryIndex, error) {
	var m PluginManifest
	if err := json.Unmarshal(manifestBody, &m); err != nil {
		return nil, fmt.Errorf("invalid plugin.json: %w", err)
	}
	namespace, shortID := parseFullId(m.ID)
	if namespace == "" {
		namespace = repoOwner(repoURL)
	}
	if !validPluginID("@" + namespace + "/" + shortID) {
		return nil, fmt.Errorf("plugin.json: id %q in namespace %q is not a valid plugin id", m.ID, namespace)
	}
	return &RepositoryIndex{
		Version:     1,
		Name:        m.Name,
		Namespace:   namespace,
		Description: m.Description,
		Author:      m.Author,
		URL:         repoURL,
		Plugins: []PluginListing{{
			ID:          shortID,
			Name:        m.Name,
			Version:     m.Version,
			Description: m.Description,
			Author:      m.Author,
			Categories:  m.Tags,
		}},
	}, nil
}

// repoOwner: the first path segment of the repository URL, lower case
func repoOwner(repoURL string) string {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return strings.ToLower(parts[0])
}

// RepoFilePath joins a plugin's directory in the repository and a file in it; a
// plugin at the repository root has no directory.
func RepoFilePath(dir, file string) string {
	if dir == "" {
		return file
	}
	return strings.TrimSuffix(dir, "/") + "/" + file
}
