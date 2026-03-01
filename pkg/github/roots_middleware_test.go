package github

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// --- RootsEnforcementMiddleware tests ---

func TestRootsEnforcementMiddleware_PassthroughNonToolsCall(t *testing.T) {
	middleware := RootsEnforcementMiddleware("github.com", testLogger())
	called := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := middleware(next)

	_, err := handler(context.Background(), "resources/list", nil)
	require.NoError(t, err)
	assert.True(t, called, "next handler should be called for non-tools/call methods")
}

func TestRootsEnforcementMiddleware_PassthroughNilSession(t *testing.T) {
	middleware := RootsEnforcementMiddleware("github.com", testLogger())
	called := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := middleware(next)

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "list_issues",
			Arguments: json.RawMessage(`{"state":"open"}`),
		},
	}
	_, err := handler(context.Background(), "tools/call", req)
	require.NoError(t, err)
	assert.True(t, called, "next handler should be called when session is nil")
}

func TestRootsEnforcementMiddleware_Integration(t *testing.T) {
	// Helper to set up server + client with enforcement middleware
	setup := func(t *testing.T, roots ...*mcp.Root) (*mcp.ClientSession, func()) {
		t.Helper()
		clientTransport, serverTransport := mcp.NewInMemoryTransports()

		server := mcp.NewServer(&mcp.Implementation{
			Name:    "test-server",
			Version: "0.1.0",
		}, nil)

		server.AddReceivingMiddleware(RootsEnforcementMiddleware("github.com", testLogger()))

		// Echo tool returns arguments back as text
		server.AddTool(&mcp.Tool{
			Name:        "echo_args",
			Description: "Echoes back arguments",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(req.Params.Arguments)},
				},
			}, nil
		})

		ctx := context.Background()

		serverSession, err := server.Connect(ctx, serverTransport, nil)
		require.NoError(t, err)

		client := mcp.NewClient(&mcp.Implementation{
			Name:    "test-client",
			Version: "0.1.0",
		}, nil)
		if len(roots) > 0 {
			client.AddRoots(roots...)
		}

		clientSession, err := client.Connect(ctx, clientTransport, nil)
		require.NoError(t, err)

		cleanup := func() {
			clientSession.Close()
			serverSession.Close()
		}
		return clientSession, cleanup
	}

	t.Run("allows tool call within single repo root", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/octocat/Hello-World"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "octocat", "repo": "Hello-World"},
		})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)
		assert.False(t, result.IsError)

		text := result.Content[0].(*mcp.TextContent).Text
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &args))
		assert.Equal(t, "octocat", args["owner"])
		assert.Equal(t, "Hello-World", args["repo"])
	})

	t.Run("rejects tool call outside repo root", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/octocat/Hello-World"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "evil-org", "repo": "secret-repo"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		text := result.Content[0].(*mcp.TextContent).Text
		assert.Contains(t, text, "root enforcement")
		assert.Contains(t, text, "evil-org")
	})

	t.Run("rejects wrong repo for same owner", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg/repo-a"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "repo-b"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		text := result.Content[0].(*mcp.TextContent).Text
		assert.Contains(t, text, "root enforcement")
		assert.Contains(t, text, "repo-b")
	})

	t.Run("allows any repo under org-level root", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "any-repo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("rejects wrong owner even with org root", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "other-org", "repo": "repo"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "other-org")
	})

	t.Run("org root plus repo root enforcement", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg"},
			&mcp.Root{URI: "https://github.com/myorg/main-app"},
		)
		defer cleanup()

		// Allowed: any repo under the org
		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "any-repo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("multiple repo roots same owner allows listed repos", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg/repo-a"},
			&mcp.Root{URI: "https://github.com/myorg/repo-b"},
		)
		defer cleanup()

		// repo-a allowed
		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "repo-a"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		// repo-b allowed
		result, err = clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "repo-b"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		// repo-c rejected
		result, err = clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "repo-c"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
	})

	t.Run("no roots means no enforcement", func(t *testing.T) {
		clientSession, cleanup := setup(t) // no roots
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "any-org", "repo": "any-repo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("tool without owner/repo passes through", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/myorg/myrepo"},
		)
		defer cleanup()

		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"state": "open"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("case-insensitive owner matching", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/MyOrg/MyRepo"},
		)
		defer cleanup()

		// Lowercase owner should match
		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "myorg", "repo": "myrepo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)
	})

	t.Run("multiple orgs allows each org", func(t *testing.T) {
		clientSession, cleanup := setup(t,
			&mcp.Root{URI: "https://github.com/org-a/repo"},
			&mcp.Root{URI: "https://github.com/org-b/repo"},
		)
		defer cleanup()

		// org-a allowed
		result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "org-a", "repo": "repo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		// org-b allowed
		result, err = clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "org-b", "repo": "repo"},
		})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		// org-c rejected
		result, err = clientSession.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"owner": "org-c", "repo": "repo"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
	})
}
