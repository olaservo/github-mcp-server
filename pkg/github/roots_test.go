package github

import (
	"testing"

	"github.com/github/github-mcp-server/internal/toolsnaps"
	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ListRootsTool(t *testing.T) {
	t.Parallel()

	serverTool := ListRootsTool(translations.NullTranslationHelper, "")
	tool := serverTool.Tool
	require.NoError(t, toolsnaps.Test(tool.Name, tool))

	assert.Equal(t, "list_roots", tool.Name)
	assert.True(t, tool.Annotations.ReadOnlyHint, "list_roots tool should be read-only")
	assert.True(t, serverTool.InsidersOnly, "list_roots tool should be insiders-only")
}

func TestParseGitHubRootURI(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		host      string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "https github.com",
			uri:       "https://github.com/octocat/Hello-World",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "git protocol",
			uri:       "git://github.com/octocat/Hello-World",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "https with .git suffix",
			uri:       "https://github.com/octocat/Hello-World.git",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "trailing slash",
			uri:       "https://github.com/octocat/Hello-World/",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "extra path segments",
			uri:       "https://github.com/octocat/Hello-World/tree/main",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "default host when empty",
			uri:       "https://github.com/octocat/Hello-World",
			host:      "",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "GitHub Enterprise",
			uri:       "https://github.enterprise.com/myorg/myrepo",
			host:      "github.enterprise.com",
			wantOwner: "myorg",
			wantRepo:  "myrepo",
		},
		{
			name:      "case insensitive host match",
			uri:       "https://GitHub.com/octocat/Hello-World",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:    "wrong host",
			uri:     "https://gitlab.com/octocat/Hello-World",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			uri:     "ftp://github.com/octocat/Hello-World",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:    "no path",
			uri:     "https://github.com",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:      "org only",
			uri:       "https://github.com/octocat",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "",
		},
		{
			name:      "org only trailing slash",
			uri:       "https://github.com/myorg/",
			host:      "github.com",
			wantOwner: "myorg",
			wantRepo:  "",
		},
		{
			name:      "org only enterprise",
			uri:       "https://github.enterprise.com/myorg",
			host:      "github.enterprise.com",
			wantOwner: "myorg",
			wantRepo:  "",
		},
		{
			name:    "empty URI",
			uri:     "",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:    "invalid URI",
			uri:     "://not-a-url",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:    "file scheme",
			uri:     "file:///home/user/repo",
			host:    "github.com",
			wantErr: true,
		},
		{
			name:      "http scheme",
			uri:       "http://github.com/octocat/Hello-World",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "git with .git suffix",
			uri:       "git://github.com/octocat/Hello-World.git",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "hello-world",
		},
		{
			name:      "mixed case owner and repo normalized",
			uri:       "https://github.com/OctoCat/My-Repo",
			host:      "github.com",
			wantOwner: "octocat",
			wantRepo:  "my-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := ParseGitHubRootURI(tt.uri, tt.host)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOwner, owner)
			assert.Equal(t, tt.wantRepo, repo)
		})
	}
}

func TestParseGitHubRoots(t *testing.T) {
	t.Run("parses valid roots", func(t *testing.T) {
		roots := []*mcp.Root{
			{URI: "https://github.com/octocat/Hello-World", Name: "Hello World"},
			{URI: "git://github.com/myorg/myrepo", Name: "My Repo"},
		}
		result := ParseGitHubRoots(roots, "github.com")
		require.Len(t, result, 2)
		assert.Equal(t, "octocat", result[0].Owner)
		assert.Equal(t, "hello-world", result[0].Repo)
		assert.Equal(t, "Hello World", result[0].Name)
		assert.Equal(t, "myorg", result[1].Owner)
		assert.Equal(t, "myrepo", result[1].Repo)
	})

	t.Run("skips non-GitHub roots", func(t *testing.T) {
		roots := []*mcp.Root{
			{URI: "https://github.com/octocat/Hello-World"},
			{URI: "file:///home/user/project"},
			{URI: "s3://my-bucket/data"},
		}
		result := ParseGitHubRoots(roots, "github.com")
		require.Len(t, result, 1)
		assert.Equal(t, "octocat", result[0].Owner)
	})

	t.Run("skips nil roots", func(t *testing.T) {
		roots := []*mcp.Root{nil, {URI: "https://github.com/octocat/Hello-World"}}
		result := ParseGitHubRoots(roots, "github.com")
		require.Len(t, result, 1)
	})

	t.Run("includes org-level roots", func(t *testing.T) {
		roots := []*mcp.Root{
			{URI: "https://github.com/myorg", Name: "My Org"},
			{URI: "https://github.com/octocat/Hello-World", Name: "Hello World"},
		}
		result := ParseGitHubRoots(roots, "github.com")
		require.Len(t, result, 2)
		assert.Equal(t, "myorg", result[0].Owner)
		assert.Equal(t, "", result[0].Repo)
		assert.Equal(t, "My Org", result[0].Name)
		assert.Equal(t, "octocat", result[1].Owner)
		assert.Equal(t, "hello-world", result[1].Repo)
	})

	t.Run("empty roots", func(t *testing.T) {
		result := ParseGitHubRoots(nil, "github.com")
		assert.Nil(t, result)
	})
}

func TestMakeOwnerRepoOptional(t *testing.T) {
	t.Run("removes owner and repo from required", func(t *testing.T) {
		tools := []inventory.ServerTool{
			{
				Tool: mcp.Tool{
					Name: "list_issues",
					InputSchema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"owner": {Type: "string", Description: "Repository owner"},
							"repo":  {Type: "string", Description: "Repository name"},
							"state": {Type: "string"},
						},
						Required: []string{"owner", "repo"},
					},
				},
			},
		}
		result := MakeOwnerRepoOptional(tools)
		require.Len(t, result, 1)

		schema := result[0].Tool.InputSchema.(*jsonschema.Schema)
		assert.Empty(t, schema.Required)
		assert.Contains(t, schema.Properties["owner"].Description, "optional when roots are configured")
		assert.Contains(t, schema.Properties["repo"].Description, "optional when roots are configured")
	})

	t.Run("preserves other required fields", func(t *testing.T) {
		tools := []inventory.ServerTool{
			{
				Tool: mcp.Tool{
					Name: "get_commit",
					InputSchema: &jsonschema.Schema{
						Type:     "object",
						Required: []string{"owner", "repo", "sha"},
					},
				},
			},
		}
		result := MakeOwnerRepoOptional(tools)
		schema := result[0].Tool.InputSchema.(*jsonschema.Schema)
		assert.Equal(t, []string{"sha"}, schema.Required)
	})

	t.Run("does not modify tools without owner/repo", func(t *testing.T) {
		tools := []inventory.ServerTool{
			{
				Tool: mcp.Tool{
					Name: "get_me",
					InputSchema: &jsonschema.Schema{
						Type:     "object",
						Required: []string{},
					},
				},
			},
		}
		result := MakeOwnerRepoOptional(tools)
		schema := result[0].Tool.InputSchema.(*jsonschema.Schema)
		assert.Empty(t, schema.Required)
	})

	t.Run("does not mutate original tools", func(t *testing.T) {
		original := []inventory.ServerTool{
			{
				Tool: mcp.Tool{
					Name: "list_issues",
					InputSchema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"owner": {Type: "string", Description: "Repository owner"},
							"repo":  {Type: "string", Description: "Repository name"},
						},
						Required: []string{"owner", "repo"},
					},
				},
			},
		}
		_ = MakeOwnerRepoOptional(original)

		// Original should be unchanged
		schema := original[0].Tool.InputSchema.(*jsonschema.Schema)
		assert.Equal(t, []string{"owner", "repo"}, schema.Required)
		assert.Equal(t, "Repository owner", schema.Properties["owner"].Description)
	})

	t.Run("skips tools with non-jsonschema InputSchema", func(t *testing.T) {
		tools := []inventory.ServerTool{
			{
				Tool: mcp.Tool{
					Name:        "raw_tool",
					InputSchema: map[string]any{"type": "object"},
				},
			},
		}
		result := MakeOwnerRepoOptional(tools)
		require.Len(t, result, 1)
		// Should pass through unchanged
		assert.Equal(t, tools[0].Tool.InputSchema, result[0].Tool.InputSchema)
	})
}
