package github

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GitHubRoot represents a parsed GitHub repository root.
type GitHubRoot struct {
	Owner string
	Repo  string
	URI   string
	Name  string
}

// ParseGitHubRootURI parses a root URI to extract the GitHub owner and repo.
// Supported formats:
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - git://github.com/owner/repo
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
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return "", "", fmt.Errorf("URI %q does not contain owner/repo (expected /owner/repo)", uri)
	}

	owner = segments[0]
	repo = segments[1]

	// Strip .git suffix from repo name
	repo = strings.TrimSuffix(repo, ".git")

	if repo == "" {
		return "", "", fmt.Errorf("URI %q has empty repo name after stripping .git", uri)
	}

	return owner, repo, nil
}

// ParseGitHubRoots parses a list of MCP roots and returns those that are valid GitHub repository URIs.
// Non-GitHub roots are silently skipped.
func ParseGitHubRoots(roots []*mcp.Root, host string) []GitHubRoot {
	var result []GitHubRoot
	for _, root := range roots {
		if root == nil {
			continue
		}
		owner, repo, err := ParseGitHubRootURI(root.URI, host)
		if err != nil {
			continue // skip non-GitHub roots
		}
		result = append(result, GitHubRoot{
			Owner: owner,
			Repo:  repo,
			URI:   root.URI,
			Name:  root.Name,
		})
	}
	return result
}

// MakeOwnerRepoOptional creates copies of tools with "owner" and "repo"
// removed from the Required list in their InputSchema.
// This is used in roots mode where owner/repo can be inferred from configured roots.
func MakeOwnerRepoOptional(tools []inventory.ServerTool) []inventory.ServerTool {
	result := make([]inventory.ServerTool, len(tools))
	for i, tool := range tools {
		result[i] = tool

		schema, ok := tool.Tool.InputSchema.(*jsonschema.Schema)
		if !ok || schema == nil {
			continue
		}

		hasOwner := slices.Contains(schema.Required, "owner")
		hasRepo := slices.Contains(schema.Required, "repo")
		if !hasOwner && !hasRepo {
			continue
		}

		// Make shallow copies to avoid mutating originals
		toolCopy := tool
		schemaCopy := *schema

		// Filter out "owner" and "repo" from Required
		newRequired := make([]string, 0, len(schemaCopy.Required))
		for _, r := range schemaCopy.Required {
			if r != "owner" && r != "repo" {
				newRequired = append(newRequired, r)
			}
		}
		schemaCopy.Required = newRequired

		// Update property descriptions to indicate they default from roots
		if schemaCopy.Properties != nil {
			newProps := make(map[string]*jsonschema.Schema, len(schemaCopy.Properties))
			for k, v := range schemaCopy.Properties {
				newProps[k] = v
			}
			if ownerProp, exists := newProps["owner"]; exists && ownerProp != nil {
				propCopy := *ownerProp
				propCopy.Description += " (optional when roots are configured)"
				newProps["owner"] = &propCopy
			}
			if repoProp, exists := newProps["repo"]; exists && repoProp != nil {
				propCopy := *repoProp
				propCopy.Description += " (optional when roots are configured)"
				newProps["repo"] = &propCopy
			}
			schemaCopy.Properties = newProps
		}

		toolCopy.Tool.InputSchema = &schemaCopy
		result[i] = toolCopy
	}
	return result
}
