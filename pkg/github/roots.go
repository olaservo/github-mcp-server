package github

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Root represents a parsed GitHub repository root.
type Root struct {
	Owner string
	Repo  string
	URI   string
	Name  string
}

// ParseGitHubRootURI parses a root URI to extract the GitHub owner and repo.
// Supported formats:
//   - https://github.com/owner/repo      (repo-level root)
//   - https://github.com/owner/repo.git  (repo-level root, .git stripped)
//   - https://github.com/owner           (org-level root, repo returned as "")
//   - git://github.com/owner/repo        (repo-level root)
//   - https://github.com/owner/repo/tree/main (extra path segments ignored)
//
// The host parameter specifies the expected GitHub host (e.g., "github.com").
// Returns an error if the URI cannot be parsed or doesn't match the expected host.
func ParseGitHubRootURI(uri string, host string) (owner, repo string, err error) {
	if host == "" {
		host = "github.com"
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid URI %q: %w", uri, err)
	}

	switch parsed.Scheme {
	case "https", "http", "git":
		// supported
	default:
		return "", "", fmt.Errorf("unsupported URI scheme %q in %q (expected https, http, or git)", parsed.Scheme, uri)
	}

	// Match the host (case-insensitive)
	if !strings.EqualFold(parsed.Host, host) {
		return "", "", fmt.Errorf("URI host %q does not match expected host %q", parsed.Host, host)
	}

	// Extract path segments: /owner/repo[/...]
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "", "", fmt.Errorf("URI %q has no path (expected /owner/repo)", uri)
	}

	segments := strings.SplitN(path, "/", 3) // at most 3: owner, repo, rest
	if len(segments) == 0 || segments[0] == "" {
		return "", "", fmt.Errorf("URI %q has no path", uri)
	}

	// GitHub usernames and org names are case-insensitive, so normalize
	// to lowercase for consistent comparison in the middleware.
	owner = strings.ToLower(segments[0])

	// If there's a second segment, treat it as the repo (repo-level root).
	// Otherwise, this is an org-level root with repo = "".
	if len(segments) >= 2 && segments[1] != "" {
		repo = strings.ToLower(strings.TrimSuffix(segments[1], ".git"))
	}

	return owner, repo, nil
}

// ParseGitHubRoots parses a list of MCP roots and returns those that are valid GitHub repository URIs.
// Non-GitHub roots are silently skipped.
func ParseGitHubRoots(roots []*mcp.Root, host string) []Root {
	var result []Root
	for _, root := range roots {
		if root == nil {
			continue
		}
		owner, repo, err := ParseGitHubRootURI(root.URI, host)
		if err != nil {
			continue // skip non-GitHub roots
		}
		result = append(result, Root{
			Owner: owner,
			Repo:  repo,
			URI:   root.URI,
			Name:  root.Name,
		})
	}
	return result
}