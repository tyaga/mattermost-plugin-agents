// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyLockedSettings(t *testing.T) {
	tests := []struct {
		name      string
		locked    [4]string // tool names, access mode, disable semantic search, track AI generated
		args      []string
		env       map[string]string
		wantErr   string
		wantTools []string
		wantMode  string
		wantNoSem bool
		wantTrack *bool
	}{
		{
			name:      "nothing locked keeps startup values",
			args:      []string{"--tool-names", "dm", "--access-mode", "local"},
			wantTools: []string{"dm"},
			wantMode:  "local",
		},
		{
			name:      "locked values apply when nothing is passed",
			locked:    [4]string{"read_channel,dm", "remote", "true", "true"},
			wantTools: []string{"read_channel", "dm"},
			wantMode:  "remote",
			wantNoSem: true,
			wantTrack: new(true),
		},
		{
			name:      "the same tool set in another order is accepted",
			locked:    [4]string{"read_channel,dm", "", "", ""},
			env:       map[string]string{"MM_TOOL_NAMES": "dm, read_channel"},
			wantTools: []string{"read_channel", "dm"},
			wantMode:  "local",
		},
		{
			name:    "a wider tool set is refused",
			locked:  [4]string{"read_channel,dm", "", "", ""},
			args:    []string{"--tool-names", "read_channel,dm,delete_post"},
			wantErr: "--tool-names / MM_TOOL_NAMES is fixed",
		},
		{
			name:    "local file access is refused when remote is locked",
			locked:  [4]string{"", "remote", "", ""},
			env:     map[string]string{"MM_ACCESS_MODE": "local"},
			wantErr: "--access-mode / MM_ACCESS_MODE is fixed",
		},
		{
			name:    "semantic search cannot be turned back on",
			locked:  [4]string{"", "", "true", ""},
			args:    []string{"--disable-semantic-search=false"},
			wantErr: "--disable-semantic-search / MM_DISABLE_SEMANTIC_SEARCH is fixed",
		},
		{
			name:    "AI tracking cannot be turned off",
			locked:  [4]string{"", "", "", "true"},
			args:    []string{"--track-ai-generated=false"},
			wantErr: "--track-ai-generated is fixed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lockedToolNames, lockedAccessMode, lockedDisableSemanticSearch, lockedTrackAIGenerated = tt.locked[0], tt.locked[1], tt.locked[2], tt.locked[3]
			toolNames, accessMode, disableSemantic, trackAIGenerated = nil, "", false, nil
			t.Cleanup(func() {
				lockedToolNames, lockedAccessMode, lockedDisableSemanticSearch, lockedTrackAIGenerated = "", "", "", ""
			})

			cmd := newRootCmd()
			require.NoError(t, cmd.ParseFlags(tt.args))
			if cmd.Flags().Changed("track-ai-generated") {
				val, _ := cmd.Flags().GetBool("track-ai-generated")
				trackAIGenerated = &val
			}
			// runServer reads these variables when the flag is not passed.
			for key, value := range tt.env {
				t.Setenv(key, value)
				switch key {
				case "MM_TOOL_NAMES":
					toolNames = splitToolNames(value)
				case "MM_ACCESS_MODE":
					accessMode = value
				}
			}

			err := applyLockedSettings(cmd)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantTools, toolNames)
			assert.Equal(t, tt.wantMode, accessMode)
			assert.Equal(t, tt.wantNoSem, disableSemantic)
			assert.Equal(t, tt.wantTrack, trackAIGenerated)
		})
	}
}
