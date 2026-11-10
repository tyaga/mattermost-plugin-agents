// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectStdio builds a stdio server against a fake Mattermost that only
// answers the startup token check and connects a client session to it.
func connectStdio(t *testing.T, config StdioConfig) (*mcp.ClientSession, error) {
	t.Helper()
	mm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/me" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"abcdefghijklmnopqrstuvwxyz","username":"jdoe"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(mm.Close)

	logger, err := loggerlib.CreateDefaultLogger()
	require.NoError(t, err)
	config.MMServerURL = mm.URL
	config.PersonalAccessToken = "token"
	server, err := NewStdioServer(config, logger, nil, nil)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = server.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, nil
}

// listStdioTools returns the tools a stdio server with config advertises.
func listStdioTools(t *testing.T, config StdioConfig) (map[string]*mcp.Tool, error) {
	t.Helper()
	session, err := connectStdio(t, config)
	if err != nil {
		return nil, err
	}
	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	tools := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools, nil
}

func schemaHasProperty(t *testing.T, tool *mcp.Tool, property string) bool {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	_, ok := schema.Properties[property]
	return ok
}

func TestStdioServerOptions(t *testing.T) {
	corporateSet := []string{"read_channel", "read_post", "create_post", "dm", "search_posts", "get_me"}

	tests := []struct {
		name            string
		config          StdioConfig
		wantTools       []string // exact set when exact is true, otherwise a subset
		exact           bool
		wantAttachments bool
		wantSemantic    bool
	}{
		{
			name:            "defaults keep local file access and semantic search",
			config:          StdioConfig{},
			wantTools:       []string{"upload_file", "read_channel", "create_post", "search_posts"},
			wantAttachments: true,
			wantSemantic:    true,
		},
		{
			name: "remote, named tools and keyword search",
			config: StdioConfig{
				BaseConfig: BaseConfig{ToolNames: corporateSet, DisableSemanticSearch: true},
				AccessMode: "remote",
			},
			wantTools: corporateSet,
			exact:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, err := listStdioTools(t, tt.config)
			require.NoError(t, err)

			names := make([]string, 0, len(tools))
			for name := range tools {
				names = append(names, name)
			}
			if tt.exact {
				assert.ElementsMatch(t, tt.wantTools, names)
			} else {
				for _, want := range tt.wantTools {
					assert.Contains(t, names, want)
				}
			}

			require.Contains(t, tools, "create_post")
			assert.Equal(t, tt.wantAttachments, schemaHasProperty(t, tools["create_post"], "attachments"))
			require.Contains(t, tools, "search_posts")
			assert.Equal(t, tt.wantSemantic, schemaHasProperty(t, tools["search_posts"], "semantic_limit"))
		})
	}

	t.Run("unknown access mode stops the server", func(t *testing.T) {
		_, err := listStdioTools(t, StdioConfig{AccessMode: "anywhere"})
		assert.Error(t, err)
	})
	t.Run("a disabled parameter leaves the schema and is refused in calls", func(t *testing.T) {
		session, err := connectStdio(t, StdioConfig{DisabledToolParams: []string{"read_channel.from"}})
		require.NoError(t, err)
		listed, err := session.ListTools(context.Background(), nil)
		require.NoError(t, err)
		for _, tool := range listed.Tools {
			if tool.Name == "read_channel" {
				assert.False(t, schemaHasProperty(t, tool, "from"))
				assert.True(t, schemaHasProperty(t, tool, "since"))
				assert.Contains(t, tool.Description, "Not available on this server: from.")
			}
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "read_channel",
			Arguments: map[string]any{"channel_id": "abcdefghijklmnopqrstuvwxyz", "from": "jdoe"},
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Content)
		text, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.True(t, result.IsError)
		assert.Contains(t, text.Text, `parameter "from" of read_channel is not available on this server`)
	})
	t.Run("remote mode refuses to upload a local file", func(t *testing.T) {
		session, err := connectStdio(t, StdioConfig{AccessMode: "remote"})
		require.NoError(t, err)
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "upload_file",
			Arguments: map[string]any{"channel_id": "abcdefghijklmnopqrstuvwxyz", "path": "/etc/hosts"},
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Content)
		text, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "remote access mode")
	})
}
