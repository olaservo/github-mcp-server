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

// --- RootsInjectionMiddleware tests ---

func TestRootsInjectionMiddleware_PassthroughNonToolsCall(t *testing.T) {
	middleware := RootsInjectionMiddleware("github.com", testLogger())
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

func TestRootsInjectionMiddleware_PassthroughNilSession(t *testing.T) {
	middleware := RootsInjectionMiddleware("github.com", testLogger())
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

func TestRootsInjectionMiddleware_Integration(t *testing.T) {
	// Create an in-memory client-server pair to test the full middleware flow.
	// The client configures roots, the server middleware reads them and injects owner/repo.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	// Set up server with a test tool and the injection middleware
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	logger := testLogger()
	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", logger))

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
		assert.Equal(t, "hello-world", args["repo"])
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

func TestRootsInjectionMiddleware_MultipleRoots(t *testing.T) {
	// When multiple roots from different orgs are configured, middleware should NOT inject owner/repo
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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
	// Add multiple GitHub roots from different orgs
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
	// With multiple roots from different orgs, owner/repo should NOT be injected
	assert.Nil(t, args["owner"])
	assert.Nil(t, args["repo"])
	assert.Equal(t, "open", args["state"])
}

func TestRootsInjectionMiddleware_OrgRoot(t *testing.T) {
	// When an org-only root is configured, middleware should inject owner only
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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

func TestRootsInjectionMiddleware_OrgPlusRepoRoot(t *testing.T) {
	// Org + single repo root: repo root wins, both owner and repo injected
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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

func TestRootsInjectionMiddleware_OrgPlusMultipleRepoRoots(t *testing.T) {
	// Org + multiple repo roots: only owner injected (ambiguous repo)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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

func TestRootsInjectionMiddleware_MultipleReposSameOrg(t *testing.T) {
	// Multiple repo roots with same org (no org root): owner injected, repo not
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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

func TestRootsInjectionMiddleware_MultipleOrgs(t *testing.T) {
	// Multiple orgs: no injection at all
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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

func TestRootsInjectionMiddleware_MixedCaseSameOrg(t *testing.T) {
	// Mixed-case owner names should be treated as the same org (GitHub is case-insensitive)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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
	// Mixed case: "MyOrg" and "myorg" should be treated as the same owner
	client.AddRoots(
		&mcp.Root{URI: "https://github.com/MyOrg/Repo-A"},
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
	// Both are same org after normalization — owner should be injected
	assert.Equal(t, "myorg", args["owner"])
	// Two repo roots — repo should NOT be injected (ambiguous)
	assert.Nil(t, args["repo"])
}

func TestRootsInjectionMiddleware_NoRoots(t *testing.T) {
	// When no roots are configured, middleware should pass through
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "0.1.0",
	}, nil)

	server.AddReceivingMiddleware(RootsInjectionMiddleware("github.com", testLogger()))

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
