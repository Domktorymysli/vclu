package plugins

import (
	"log"
	"regexp"
	"strings"
)

var validTagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// isValidGitRef checks if a string is a safe git tag/ref name
func isValidGitRef(ref string) bool {
	return ref != "" && len(ref) < 128 && validTagRe.MatchString(ref) &&
		!strings.Contains(ref, "..")
}

// resolveRef returns the ref to use for URL building.
// Empty or invalid refs fall back to "main".
func resolveRef(ref string) string {
	if ref == "" {
		return "main"
	}
	if !isValidGitRef(ref) {
		log.Printf("[PLUGINS] WARNING: invalid gitTag %q, falling back to main", ref)
		return "main"
	}
	return ref
}

// RepoResolver builds URLs for different repository hosting platforms
type RepoResolver interface {
	RawFileURL(repoURL, ref, path string) string
	BrowseURL(repoURL, ref, path string) string
}

// GitHubResolver builds URLs for GitHub repositories
type GitHubResolver struct{}

func (g *GitHubResolver) RawFileURL(repoURL, ref, path string) string {
	ref = resolveRef(ref)
	url := strings.Replace(repoURL, "github.com", "raw.githubusercontent.com", 1)
	return strings.TrimSuffix(url, "/") + "/" + ref + "/" + path
}

func (g *GitHubResolver) BrowseURL(repoURL, ref, path string) string {
	ref = resolveRef(ref)
	return strings.TrimSuffix(repoURL, "/") + "/tree/" + ref + "/" + path
}

// GenericResolver builds URLs for non-GitHub repositories (ignores ref)
type GenericResolver struct{}

func (g *GenericResolver) RawFileURL(repoURL, _, path string) string {
	return strings.TrimSuffix(repoURL, "/") + "/" + path
}

func (g *GenericResolver) BrowseURL(repoURL, _, path string) string {
	return strings.TrimSuffix(repoURL, "/") + "/" + path
}

// DefaultResolver returns the appropriate resolver for the given repository URL
func DefaultResolver(repoURL string) RepoResolver {
	if strings.Contains(repoURL, "github.com") {
		return &GitHubResolver{}
	}
	return &GenericResolver{}
}
