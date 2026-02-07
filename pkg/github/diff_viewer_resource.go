package github

import (
	"context"
	_ "embed"

	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed ui/dist/mcp-app.html
var diffViewerHTML string

const (
	diffViewerResourceURI  = "ui://github-mcp/diff-viewer.html"
	diffViewerMIMEType     = "text/html;profile=mcp-app"
)

// GetDiffViewerResource returns the resource template for the diff viewer UI.
func GetDiffViewerResource(t translations.TranslationHelperFunc) inventory.ServerResourceTemplate {
	return inventory.NewServerResourceTemplate(
		ToolsetMetadataPullRequests,
		mcp.ResourceTemplate{
			Name:        "diff_viewer_ui",
			URITemplate: diffViewerResourceURI,
			Description: t("RESOURCE_DIFF_VIEWER_DESCRIPTION", "Interactive diff viewer for pull requests"),
			MIMEType:    diffViewerMIMEType,
		},
		func(_ any) mcp.ResourceHandler {
			return func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{
					Contents: []*mcp.ResourceContents{
						{
							URI:      diffViewerResourceURI,
							MIMEType: diffViewerMIMEType,
							Text:     diffViewerHTML,
						},
					},
				}, nil
			}
		},
	)
}
