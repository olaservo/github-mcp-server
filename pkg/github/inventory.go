package github

import (
	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/translations"
)

// NewInventory creates an Inventory with all available tools, resources, and prompts.
// Tools, resources, and prompts are self-describing with their toolset metadata embedded.
// This function is stateless - no dependencies are captured.
// Handlers are generated on-demand during registration via RegisterAll(ctx, server, deps).
// The "default" keyword in WithToolsets will expand to toolsets marked with Default: true.
func NewInventory(t translations.TranslationHelperFunc, opts ...InventoryOption) *inventory.Builder {
	cfg := inventoryConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	tools := AllTools(t, cfg.host)
	if cfg.rootsMode {
		tools = MakeOwnerRepoOptional(tools)
	}
	return inventory.NewBuilder().
		SetTools(tools).
		SetResources(AllResources(t)).
		SetPrompts(AllPrompts(t))
}

// inventoryConfig holds configuration options for building the inventory.
type inventoryConfig struct {
	host      string
	rootsMode bool
}

// InventoryOption configures inventory building.
type InventoryOption func(*inventoryConfig)

// WithHost sets the GitHub host for tool configuration.
func WithHost(host string) InventoryOption {
	return func(c *inventoryConfig) { c.host = host }
}

// WithRootsMode enables roots mode, making owner/repo optional in tool schemas.
func WithRootsMode(enabled bool) InventoryOption {
	return func(c *inventoryConfig) { c.rootsMode = enabled }
}
