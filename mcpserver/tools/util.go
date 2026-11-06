// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// apiParallelism bounds how many Mattermost API requests one tool call has
// in flight at once when it fans out per-item lookups.
const apiParallelism = 8

// resolveUserID returns the authenticated user's ID. It prefers the ID already
// resolved on the context (set by external servers) and falls back to GetMe so
// "me"-scoped tools work on every auth provider.
func (p *MattermostToolProvider) resolveUserID(mcpContext *MCPToolContext) (string, error) {
	if mcpContext.UserID != "" {
		return mcpContext.UserID, nil
	}
	user, err := p.resolveUser(mcpContext)
	if err != nil {
		return "", err
	}
	return user.Id, nil
}

// resolveUser returns the authenticated user, from the context when the auth
// provider resolved it and from GetMe otherwise.
func (p *MattermostToolProvider) resolveUser(mcpContext *MCPToolContext) (*model.User, error) {
	if mcpContext.User != nil {
		return mcpContext.User, nil
	}
	user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, "")
	if err != nil {
		return nil, fmt.Errorf("error getting current user: %w", err)
	}
	return user, nil
}

// resolveUserIDOrDefault returns userID when set, otherwise the authenticated
// user's ID, for tools whose user_id argument defaults to the caller.
func (p *MattermostToolProvider) resolveUserIDOrDefault(mcpContext *MCPToolContext, userID string) (string, error) {
	if userID != "" {
		return userID, nil
	}
	return p.resolveUserID(mcpContext)
}

// parseTimeMillis parses an RFC3339 timestamp into Unix milliseconds, matching the
// timestamp handling used by read_channel's `since` argument.
func parseTimeMillis(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp format (expected ISO 8601 / RFC3339, e.g. 2024-01-01T15:00:00Z): %w", err)
	}
	return parsed.UnixMilli(), nil
}

// usernameFor returns a user's username, or "Unknown User" when the lookup fails.
func (p *MattermostToolProvider) usernameFor(ctx context.Context, client *model.Client4, userID string) string {
	if userID == "" {
		return "Unknown User"
	}
	user, _, err := client.GetUser(ctx, userID, "")
	if err != nil || user == nil {
		p.logger.Warn("failed to get user", "user_id", userID, "error", err)
		return "Unknown User"
	}
	return user.Username
}

// usersByIDs fetches the given users in one request and returns them keyed by
// ID. Duplicates and empty IDs are ignored; users the lookup did not return
// are absent from the map. A failed request yields an empty map and a warning
// so callers degrade to "Unknown User" instead of failing the tool.
func (p *MattermostToolProvider) usersByIDs(ctx context.Context, client *model.Client4, userIDs []string) map[string]*model.User {
	unique := make([]string, 0, len(userIDs))
	seen := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	byID := make(map[string]*model.User, len(unique))
	if len(unique) == 0 {
		return byID
	}

	users, _, err := client.GetUsersByIds(ctx, unique)
	if err != nil {
		p.logger.Warn("failed to fetch users by IDs", "count", len(unique), "error", err)
		return byID
	}
	for _, user := range users {
		byID[user.Id] = user
	}
	return byID
}

// usernamesByIDs is usersByIDs reduced to usernames, with "Unknown User" for
// every requested ID the lookup did not return.
func (p *MattermostToolProvider) usernamesByIDs(ctx context.Context, client *model.Client4, userIDs []string) map[string]string {
	users := p.usersByIDs(ctx, client, userIDs)
	names := make(map[string]string, len(userIDs))
	for _, id := range userIDs {
		if user, ok := users[id]; ok {
			names[id] = user.Username
		} else {
			names[id] = "Unknown User"
		}
	}
	return names
}

// forEachParallel calls fn for every index in [0, n) from at most apiParallelism
// goroutines at a time and returns once all calls finished. fn must only write
// to its own slot of any shared slice.
func forEachParallel(n int, fn func(i int)) {
	slots := make(chan struct{}, apiParallelism)
	var wg sync.WaitGroup
	for i := range n {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			fn(i)
		})
	}
	wg.Wait()
}
