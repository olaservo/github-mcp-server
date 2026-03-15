package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RootsEnforcementMiddleware returns middleware that validates tool call arguments
// against MCP roots. When GitHub roots are configured, it checks that any
// owner/repo arguments in tool calls match at least one configured root:
//
//   - If the tool call has an "owner" arg, at least one root must have that owner
//   - If the tool call also has a "repo" arg, either an org-level root matches
//     the owner (any repo allowed) or a repo-level root matches owner+repo exactly
//   - Tools without owner/repo args pass through unmodified
//   - If no GitHub roots are configured or ListRoots fails, no enforcement applies
//
// Enforcement errors are returned as CallToolResult with IsError: true, which
// the LLM can see and react to (not hard JSON-RPC errors).
func RootsEnforcementMiddleware(host string, logger *slog.Logger) mcp.Middleware {
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
				// Client may not support roots — continue without enforcement
				logger.Debug("roots enforcement: ListRoots failed, continuing without enforcement", "error", err)
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

			// Extract owner and repo from tool call arguments
			var args map[string]any
			if len(callReq.Params.Arguments) > 0 {
				if err := json.Unmarshal(callReq.Params.Arguments, &args); err != nil {
					// Can't parse arguments — let the handler deal with it
					return next(ctx, method, request)
				}
			}

			ownerRaw, hasOwner := args["owner"]
			if !hasOwner {
				// Tool doesn't use owner/repo — no enforcement needed
				return next(ctx, method, request)
			}

			owner, ok := ownerRaw.(string)
			if !ok || owner == "" {
				return next(ctx, method, request)
			}

			// Normalize for comparison
			ownerLower := strings.ToLower(owner)

			// Check that at least one root matches the owner
			ownerAllowed := false
			hasOrgRoot := false // org-level root for this owner
			for _, r := range ghRoots {
				if r.Owner == ownerLower {
					ownerAllowed = true
					if r.Repo == "" {
						hasOrgRoot = true
					}
				}
			}

			if !ownerAllowed {
				// Build allowed owners list for the error message
				owners := make(map[string]bool)
				for _, r := range ghRoots {
					owners[r.Owner] = true
				}
				allowedList := make([]string, 0, len(owners))
				for o := range owners {
					allowedList = append(allowedList, fmt.Sprintf("%q", o))
				}
				msg := fmt.Sprintf("root enforcement: owner %q is not within configured roots (allowed owners: %s)", owner, strings.Join(allowedList, ", "))
				logger.Info(msg, "tool", callReq.Params.Name, "owner", owner)
				return utils.NewToolResultError(msg), nil
			}

			// If repo is specified, validate it against roots
			repoRaw, hasRepo := args["repo"]
			if hasRepo {
				repo, ok := repoRaw.(string)
				if ok && repo != "" {
					repoLower := strings.ToLower(repo)

					// Org-level root allows any repo under that owner
					if !hasOrgRoot {
						// Must have a repo-level root that matches exactly
						repoAllowed := false
						for _, r := range ghRoots {
							if r.Owner == ownerLower && r.Repo == repoLower {
								repoAllowed = true
								break
							}
						}
						if !repoAllowed {
							// Build allowed repos list
							var allowedRepos []string
							for _, r := range ghRoots {
								if r.Owner == ownerLower && r.Repo != "" {
									allowedRepos = append(allowedRepos, fmt.Sprintf("%q", r.Repo))
								}
							}
							msg := fmt.Sprintf("root enforcement: repository %q/%q is not within configured roots (allowed repos for %q: %s)", owner, repo, owner, strings.Join(allowedRepos, ", "))
							logger.Info(msg, "tool", callReq.Params.Name, "owner", owner, "repo", repo)
							return utils.NewToolResultError(msg), nil
						}
					}
				}
			}

			return next(ctx, method, request)
		}
	}
}

// RootsInjectionMiddleware returns middleware that injects owner/repo defaults
// from MCP roots into tool call arguments. It uses a priority model:
//   - If all GitHub roots share the same owner, inject owner when missing
//   - If exactly one repo-level root exists, also inject repo when missing
//   - If roots span multiple owners, no injection (fully ambiguous)
//
// This middleware is the outer (first-to-execute) middleware, running before
// RootsEnforcementMiddleware. It fills in missing values so that enforcement
// always sees the complete set of arguments. Injected values come from roots,
// so they are inherently valid and will pass enforcement.
func RootsInjectionMiddleware(host string, logger *slog.Logger) mcp.Middleware {
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
				logger.Debug("roots injection: ListRoots failed, continuing without defaults", "error", err)
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
				logger.Warn("roots injection: failed to marshal modified arguments", "error", err)
				return next(ctx, method, request)
			}
			callReq.Params.Arguments = newArgs

			// Log at Info level so auto-injection is visible, especially for
			// write operations where silently targeting a repo could be surprising.
			logAttrs := []any{"tool", callReq.Params.Name, "owner", commonOwner}
			if len(repoRoots) == 1 {
				logAttrs = append(logAttrs, "repo", repoRoots[0].Repo)
			}
			logger.Info("roots injection: injected defaults from roots", logAttrs...)

			return next(ctx, method, request)
		}
	}
}
