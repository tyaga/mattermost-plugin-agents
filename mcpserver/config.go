// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

// BaseConfig represents common configuration for all MCP server types
type BaseConfig struct {
	// Mattermost server URL (e.g., "https://mattermost.company.com")
	MMServerURL string `json:"mm_server_url"`

	// Internal Mattermost server URL for API communication (e.g., "http://localhost:8065")
	// If empty, MMServerURL will be used for internal communication
	MMInternalServerURL string `json:"mm_internal_server_url"`

	// Development mode enables additional tools for setting up test data
	DevMode bool `json:"dev_mode"`

	// TrackAIGenerated controls whether to add ai_generated_by props to posts
	// For embedded servers: uses Bot UserID from metadata
	// For external servers: uses authenticated user's ID
	// Default: true for embedded, true for HTTP, false for stdio
	TrackAIGenerated *bool `json:"track_ai_generated,omitempty"`

	// CompactOutput renders posts in tool results as a one-line header plus
	// the message instead of one labeled line per field.
	CompactOutput bool `json:"compact_output,omitempty"`

	// ToolGroups limits the registered tools to these groups (see
	// tools.ToolGroupNames); empty registers every group.
	ToolGroups []string `json:"tool_groups,omitempty"`

	// ReadOnlyTools registers only tools that retrieve data.
	ReadOnlyTools bool `json:"read_only_tools,omitempty"`

	// ToolNames limits the registered tools to these names (see
	// tools.AllToolNames); empty registers every tool. Applied together with
	// ToolGroups and ReadOnlyTools.
	ToolNames []string `json:"tool_names,omitempty"`

	// DisableSemanticSearch keeps search_posts to keyword search, as on a
	// server where the Agents plugin has no semantic search.
	DisableSemanticSearch bool `json:"disable_semantic_search,omitempty"`

	// DisabledToolParams lists "tool.param" entries (e.g. "read_channel.from")
	// dropped from tool schemas and refused in calls.
	DisabledToolParams []string `json:"disabled_tool_params,omitempty"`
}

// GetMMServerURL returns the Mattermost server URL
func (c BaseConfig) GetMMServerURL() string {
	return c.MMServerURL
}

// GetMMInternalServerURL returns the internal Mattermost server URL for API communication
// If not set, falls back to the external server URL for backward compatibility
func (c BaseConfig) GetMMInternalServerURL() string {
	if c.MMInternalServerURL != "" {
		return c.MMInternalServerURL
	}
	return c.MMServerURL
}

// GetDevMode returns the development mode setting
func (c BaseConfig) GetDevMode() bool {
	return c.DevMode
}

// GetToolGroups returns the tool groups to register; empty means all.
func (c BaseConfig) GetToolGroups() []string {
	return c.ToolGroups
}

// GetReadOnlyTools returns whether only data-retrieving tools are registered.
func (c BaseConfig) GetReadOnlyTools() bool {
	return c.ReadOnlyTools
}

// GetToolNames returns the tool names to register; empty means all.
func (c BaseConfig) GetToolNames() []string {
	return c.ToolNames
}

// GetDisabledToolParams returns the "tool.param" entries to disable.
func (c BaseConfig) GetDisabledToolParams() []string {
	return c.DisabledToolParams
}

// GetCompactOutput returns whether tool results use the compact post layout.
func (c BaseConfig) GetCompactOutput() bool {
	return c.CompactOutput
}

// GetTrackAIGenerated returns whether to track AI-generated content
// Defaults to true (always track) if not explicitly set
func (c BaseConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return true // Default to tracking
	}
	return *c.TrackAIGenerated
}

// StdioConfig represents configuration for STDIO transport MCP server
type StdioConfig struct {
	BaseConfig

	// Personal Access Token for authentication
	PersonalAccessToken string `json:"personal_access_token"`

	// AccessMode is "local" (default: tools may read local files for uploads
	// and attachments) or "remote" (no local file access, as on the plugin
	// endpoint).
	AccessMode string `json:"access_mode,omitempty"`
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For stdio, defaults to false (disabled) unless explicitly enabled
func (c StdioConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return false // Default to NOT tracking for stdio
	}
	return *c.TrackAIGenerated
}

// HTTPConfig represents configuration for HTTP transport MCP server
type HTTPConfig struct {
	BaseConfig

	// HTTP server configuration
	HTTPPort     int    `json:"http_port"`      // Port for HTTP server (default: 8080)
	HTTPBindAddr string `json:"http_bind_addr"` // Bind address (default: "127.0.0.1" for security)
	SiteURL      string `json:"site_url"`       // Site URL for external access (optional)
	Stateless    bool   `json:"stateless"`      // Stateless mode for multi-node/HA deployments (default: false)
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For HTTP, defaults to true (enabled) unless explicitly disabled
func (c HTTPConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return true // Default to tracking for HTTP
	}
	return *c.TrackAIGenerated
}

// InMemoryConfig represents configuration for in-memory transport MCP server
// Used for embedded MCP servers that run within the same process as the plugin
type InMemoryConfig struct {
	BaseConfig
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For embedded/in-memory servers, always returns true (always track)
func (c InMemoryConfig) GetTrackAIGenerated() bool {
	// Always track for embedded servers, regardless of config
	return true
}
