package github

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RootsMiddleware returns middleware that injects owner/repo defaults from MCP roots
// into tool call arguments. It uses a priority model:
//   - If all GitHub roots share the same owner, inject owner when missing
//   - If exactly one repo-level root exists, also inject repo when missing
//   - If roots span multiple owners, no injection (fully ambiguous)
//
// IMPORTANT: This middleware is a convenience layer, not a security boundary.
// It provides default values for missing parameters but does NOT restrict access.
// Tool calls with explicit owner/repo arguments bypass roots entirely — any
// repository accessible by the configured token can still be targeted.
// For access control, use appropriately scoped tokens (e.g., fine-grained PATs).
func RootsMiddleware(host string, logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}

			callReq, ok := request.(*mcp.CallToolRequest)
			if !ok || callReq.Session == nil {
				return next(ctx, method, request)
			}

			// List roots from the client
			rootsResult, err := callReq.Session.ListRoots(ctx, nil)
			if err != nil {
				// Client may not support roots — continue without injection
				logger.Debug("roots middleware: ListRoots failed, continuing without root defaults", "error", err)
				return next(ctx, method, request)
			}

			if rootsResult == nil || len(rootsResult.Roots) == 0 {
				return next(ctx, method, request)
			}

			// Parse GitHub roots
			ghRoots := ParseGitHubRoots(rootsResult.Roots, host)
			if len(ghRoots) == 0 {
				return next(ctx, method, request)
			}

			// Separate repo-level and org-level roots
			var repoRoots []Root
			for _, r := range ghRoots {
				if r.Repo != "" {
					repoRoots = append(repoRoots, r)
				}
			}

			// Check if all roots share the same owner
			commonOwner := ghRoots[0].Owner
			allSameOwner := true
			for _, r := range ghRoots[1:] {
				if r.Owner != commonOwner {
					allSameOwner = false
					break
				}
			}

			if !allSameOwner {
				// Different orgs — fully ambiguous, no injection
				return next(ctx, method, request)
			}

			// Unmarshal existing arguments
			var args map[string]any
			if len(callReq.Params.Arguments) > 0 {
				if err := json.Unmarshal(callReq.Params.Arguments, &args); err != nil {
					// Can't parse arguments — let the handler deal with it
					return next(ctx, method, request)
				}
			}
			if args == nil {
				args = make(map[string]any)
			}

			// All roots share the same owner — inject it if missing
			modified := false
			if _, hasOwner := args["owner"]; !hasOwner {
				args["owner"] = commonOwner
				modified = true
			}

			// Inject repo only if exactly 1 repo-level root exists
			if len(repoRoots) == 1 {
				if _, hasRepo := args["repo"]; !hasRepo {
					args["repo"] = repoRoots[0].Repo
					modified = true
				}
			}

			if !modified {
				return next(ctx, method, request)
			}

			// Re-marshal and update the request
			newArgs, err := json.Marshal(args)
			if err != nil {
				logger.Warn("roots middleware: failed to marshal modified arguments", "error", err)
				return next(ctx, method, request)
			}
			callReq.Params.Arguments = newArgs

			// Log at Info level so auto-injection is visible, especially for
			// write operations where silently targeting a repo could be surprising.
			logAttrs := []any{"tool", callReq.Params.Name, "owner", commonOwner}
			if len(repoRoots) == 1 {
				logAttrs = append(logAttrs, "repo", repoRoots[0].Repo)
			}
			logger.Info("roots middleware: injected defaults from roots", logAttrs...)

			return next(ctx, method, request)
		}
	}
}
