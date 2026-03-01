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

func TestRootsMiddleware_PassthroughNonToolsCall(t *testing.T) {
	middleware := RootsMiddleware("github.com", testLogger())
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

func TestRootsMiddleware_PassthroughNilSession(t *testing.T) {
	middleware := RootsMiddleware("github.com", testLogger())
	called := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	}
	handler := middleware(next)

	// Create a CallToolRequest with nil session
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

func TestRootsMiddleware_Integration(t *testing.T) {
	// Create an in-memory client-server pair to test the full middleware flow.
	// The client configures roots, the server middleware reads them and injects owner/repo.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	// Set up server with a test tool and the roots middleware
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	logger := testLogger()
	server.AddReceivingMiddleware(RootsMiddleware("github.com", logger))

	// Add a test tool that echoes back its arguments
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

	// Connect server
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()

	// Create client with a root configured
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(&mcp.Root{
		URI:  "https://github.com/octocat/Hello-World",
		Name: "Hello World repo",
	})

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	t.Run("injects owner/repo when missing", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"state": "open"},
		})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)

		text := result.Content[0].(*mcp.TextContent).Text
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &args))
		assert.Equal(t, "octocat", args["owner"])
		assert.Equal(t, "Hello-World", args["repo"])
		assert.Equal(t, "open", args["state"])
	})

	t.Run("does not override explicit owner/repo", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "echo_args",
			Arguments: map[string]any{
				"owner": "myorg",
				"repo":  "myrepo",
			},
		})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)

		text := result.Content[0].(*mcp.TextContent).Text
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &args))
		assert.Equal(t, "myorg", args["owner"])
		assert.Equal(t, "myrepo", args["repo"])
	})
}

func TestRootsMiddleware_MultipleRoots(t *testing.T) {
	// When multiple roots from different orgs are configured, middleware should NOT inject owner/repo
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	// Add multiple GitHub roots
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/octocat/Hello-World"},
		&mcp.Root{URI: "https://github.com/myorg/myrepo"},
	)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"state": "open"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	// With multiple roots, owner/repo should NOT be injected
	assert.Nil(t, args["owner"])
	assert.Nil(t, args["repo"])
	assert.Equal(t, "open", args["state"])
}

func TestRootsMiddleware_OrgRoot(t *testing.T) {
	// When an org-only root is configured, middleware should inject owner only
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(&mcp.Root{
		URI:  "https://github.com/myorg",
		Name: "My Org",
	})

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	t.Run("injects owner only", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"repo": "specific-repo"},
		})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)

		text := result.Content[0].(*mcp.TextContent).Text
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &args))
		assert.Equal(t, "myorg", args["owner"])
		assert.Equal(t, "specific-repo", args["repo"])
	})

	t.Run("does not inject repo", func(t *testing.T) {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"state": "open"},
		})
		require.NoError(t, err)
		require.Len(t, result.Content, 1)

		text := result.Content[0].(*mcp.TextContent).Text
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &args))
		assert.Equal(t, "myorg", args["owner"])
		assert.Nil(t, args["repo"]) // repo should NOT be injected for org-only root
	})
}

func TestRootsMiddleware_OrgPlusRepoRoot(t *testing.T) {
	// Org + single repo root: repo root wins, both owner and repo injected
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/myorg"},
		&mcp.Root{URI: "https://github.com/myorg/main-app"},
	)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"state": "open"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	assert.Equal(t, "myorg", args["owner"])
	assert.Equal(t, "main-app", args["repo"])
}

func TestRootsMiddleware_OrgPlusMultipleRepoRoots(t *testing.T) {
	// Org + multiple repo roots: only owner injected (ambiguous repo)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/myorg"},
		&mcp.Root{URI: "https://github.com/myorg/repo-a"},
		&mcp.Root{URI: "https://github.com/myorg/repo-b"},
	)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"repo": "repo-a"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	assert.Equal(t, "myorg", args["owner"])
	assert.Equal(t, "repo-a", args["repo"]) // explicit repo preserved, not overridden
}

func TestRootsMiddleware_MultipleReposSameOrg(t *testing.T) {
	// Multiple repo roots with same org (no org root): owner injected, repo not
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/myorg/repo-a"},
		&mcp.Root{URI: "https://github.com/myorg/repo-b"},
	)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"state": "open"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	assert.Equal(t, "myorg", args["owner"])
	assert.Nil(t, args["repo"]) // ambiguous repo, should not inject
}

func TestRootsMiddleware_MultipleOrgs(t *testing.T) {
	// Multiple orgs: no injection at all
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/org-a/repo"},
		&mcp.Root{URI: "https://github.com/org-b/repo"},
	)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"owner": "org-a", "repo": "repo"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	// Explicit values should be preserved, no injection
	assert.Equal(t, "org-a", args["owner"])
	assert.Equal(t, "repo", args["repo"])
}

func TestRootsMiddleware_NoRoots(t *testing.T) {
	// When no roots are configured, middleware should pass through
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsMiddleware("github.com", testLogger()))

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
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	// No roots configured

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"state": "open"},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text := result.Content[0].(*mcp.TextContent).Text
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &args))
	// No roots means no injection
	assert.Nil(t, args["owner"])
	assert.Nil(t, args["repo"])
}
