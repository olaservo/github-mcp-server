package github

import (
	"context"
	"encoding/json"

	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rootInfo is the JSON structure returned by the list_roots tool.
type rootInfo struct {
	URI   string `json:"uri"`
	Name  string `json:"name,omitempty"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
}

// ListRootsTool creates a tool that lists the MCP roots configured by the client
// and shows the parsed GitHub owner/repo for each root.
func ListRootsTool(t translations.TranslationHelperFunc, host string) inventory.ServerTool {
	tool := NewTool(
		ToolsetMetadataContext,
		mcp.Tool{
			Name:        "list_roots",
			Description: t("TOOL_LIST_ROOTS_DESCRIPTION", "List the MCP roots configured by the client. Shows the root URIs and any parsed GitHub owner/repo information. Use this to understand which repositories are in scope."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_LIST_ROOTS_USER_TITLE", "List configured roots"),
				ReadOnlyHint: true,
			},
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		nil, // no specific scopes required
		func(ctx context.Context, _ ToolDependencies, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
			if req.Session == nil {
				return utils.NewToolResultError("no session available"), nil, nil
			}

			rootsResult, err := req.Session.ListRoots(ctx, nil)
			if err != nil {
				return utils.NewToolResultError("failed to list roots: " + err.Error()), nil, nil
			}

			if rootsResult == nil || len(rootsResult.Roots) == 0 {
				return utils.NewToolResultText("No roots configured"), nil, nil
			}

			var infos []rootInfo
			for _, root := range rootsResult.Roots {
				if root == nil {
					continue
				}
				info := rootInfo{
					URI:  root.URI,
					Name: root.Name,
				}
				// Try to parse as GitHub root
				owner, repo, err := ParseGitHubRootURI(root.URI, host)
				if err == nil {
					info.Owner = owner
					info.Repo = repo
				}
				infos = append(infos, info)
			}

			return MarshalledTextResult(infos), nil, nil
		},
	)
	tool.InsidersOnly = true
	return tool
}
