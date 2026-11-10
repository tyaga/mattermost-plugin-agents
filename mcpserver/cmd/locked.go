// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Settings fixed at build time, for binaries distributed with a policy their
// users must not widen:
//
//	go build -ldflags "-X main.lockedToolNames=read_channel,dm -X main.lockedAccessMode=remote \
//	    -X main.lockedDisableSemanticSearch=true -X main.lockedTrackAIGenerated=true \
//	    -X main.lockedDisableToolParams=read_channel.from \
//	    -X main.lockedServerURL=https://mattermost.example.com -X main.lockedTransport=stdio"
//
// A set value overrides its flag and environment variable. A different value
// passed at startup stops the server instead of being silently ignored.
var (
	lockedToolNames             string
	lockedAccessMode            string
	lockedDisableSemanticSearch string
	lockedTrackAIGenerated      string
	lockedDisableToolParams     string
	// lockedServerURL is the only server the access token is sent to: it also
	// rules out a different internal server URL.
	lockedServerURL string
	lockedTransport string
)

// providedAtStartup reports whether the user set a flag or its environment
// variable.
func providedAtStartup(cmd *cobra.Command, flag, envVar string) bool {
	if cmd.Flags().Changed(flag) {
		return true
	}
	return envVar != "" && os.Getenv(envVar) != ""
}

func lockedError(flag, envVar, value string) error {
	if envVar == "" {
		return fmt.Errorf("--%s is fixed in this build to %q", flag, value)
	}
	return fmt.Errorf("--%s / %s is fixed in this build to %q", flag, envVar, value)
}

func splitToolNames(value string) []string {
	var names []string
	for name := range strings.SplitSeq(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func sameToolNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// applyLockedSettings replaces the startup values of locked settings with the
// build-time ones; it runs after flags and environment variables are read.
func applyLockedSettings(cmd *cobra.Command) error {
	if lockedToolNames != "" {
		want := splitToolNames(lockedToolNames)
		if providedAtStartup(cmd, "tool-names", "MM_TOOL_NAMES") && !sameToolNames(toolNames, want) {
			return lockedError("tool-names", "MM_TOOL_NAMES", lockedToolNames)
		}
		toolNames = want
	}

	if lockedAccessMode != "" {
		if providedAtStartup(cmd, "access-mode", "MM_ACCESS_MODE") && accessMode != lockedAccessMode {
			return lockedError("access-mode", "MM_ACCESS_MODE", lockedAccessMode)
		}
		accessMode = lockedAccessMode
	}

	if lockedDisableSemanticSearch != "" {
		want, err := strconv.ParseBool(lockedDisableSemanticSearch)
		if err != nil {
			return fmt.Errorf("invalid build-time disable-semantic-search %q: %w", lockedDisableSemanticSearch, err)
		}
		if providedAtStartup(cmd, "disable-semantic-search", "MM_DISABLE_SEMANTIC_SEARCH") && disableSemantic != want {
			return lockedError("disable-semantic-search", "MM_DISABLE_SEMANTIC_SEARCH", lockedDisableSemanticSearch)
		}
		disableSemantic = want
	}

	if lockedTrackAIGenerated != "" {
		want, err := strconv.ParseBool(lockedTrackAIGenerated)
		if err != nil {
			return fmt.Errorf("invalid build-time track-ai-generated %q: %w", lockedTrackAIGenerated, err)
		}
		if providedAtStartup(cmd, "track-ai-generated", "") && (trackAIGenerated == nil || *trackAIGenerated != want) {
			return lockedError("track-ai-generated", "", lockedTrackAIGenerated)
		}
		trackAIGenerated = &want
	}

	if lockedDisableToolParams != "" {
		want := splitToolNames(lockedDisableToolParams)
		if providedAtStartup(cmd, "disable-tool-params", "MM_DISABLE_TOOL_PARAMS") && !sameToolNames(disabledToolParams, want) {
			return lockedError("disable-tool-params", "MM_DISABLE_TOOL_PARAMS", lockedDisableToolParams)
		}
		disabledToolParams = want
	}

	if lockedServerURL != "" {
		if mmServerURL != "" && !sameURL(mmServerURL, lockedServerURL) {
			return lockedError("server-url", "MM_SERVER_URL", lockedServerURL)
		}
		if mmInternalServerURL != "" && !sameURL(mmInternalServerURL, lockedServerURL) {
			return lockedError("internal-server-url", "MM_INTERNAL_SERVER_URL", lockedServerURL)
		}
		mmServerURL = lockedServerURL
		mmInternalServerURL = ""
	}

	if lockedTransport != "" {
		if transport != lockedTransport {
			return lockedError("transport", "", lockedTransport)
		}
	}
	return nil
}

func sameURL(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// describeLockedSettings marks locked flags in --help.
func describeLockedSettings(cmd *cobra.Command) {
	for flag, value := range map[string]string{
		"tool-names":              lockedToolNames,
		"access-mode":             lockedAccessMode,
		"disable-semantic-search": lockedDisableSemanticSearch,
		"track-ai-generated":      lockedTrackAIGenerated,
		"disable-tool-params":     lockedDisableToolParams,
		"server-url":              lockedServerURL,
		"internal-server-url":     lockedServerURL,
		"transport":               lockedTransport,
	} {
		if f := cmd.Flags().Lookup(flag); f != nil && value != "" {
			f.Usage += fmt.Sprintf(" [fixed in this build: %s]", value)
		}
	}
}
