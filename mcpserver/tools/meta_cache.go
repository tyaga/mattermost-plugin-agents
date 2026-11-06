// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// metaCacheTTL bounds how long a team or channel record is served from
// memory. Names and types of teams and channels change rarely, while nearly
// every tool that lists posts resolves them for display.
const metaCacheTTL = 10 * time.Minute

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

// metaCache memoizes team and channel lookups per authenticated user. Entries
// are keyed by the requesting user as well as the record ID so one user's
// visibility never serves another's request on a shared (plugin-hosted)
// server. The zero value is ready to use.
type metaCache struct {
	mu       sync.Mutex
	teams    map[string]cacheEntry[*model.Team]
	channels map[string]cacheEntry[*model.Channel]
	now      func() time.Time
}

func (c *metaCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func cacheKey(userID, id string) string {
	return userID + ":" + id
}

func lookup[T any](c *metaCache, entries map[string]cacheEntry[T], key string) (T, bool) {
	entry, ok := entries[key]
	if !ok || !c.clock().Before(entry.expires) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

// team returns the team with the given ID, from cache when fresh.
func (p *MattermostToolProvider) team(ctx context.Context, client *model.Client4, userID, teamID string) (*model.Team, error) {
	c := &p.cache
	key := cacheKey(userID, teamID)

	c.mu.Lock()
	team, ok := lookup(c, c.teams, key)
	c.mu.Unlock()
	if ok {
		return team, nil
	}

	team, _, err := client.GetTeam(ctx, teamID, "")
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.teams == nil {
		c.teams = make(map[string]cacheEntry[*model.Team])
	}
	c.teams[key] = cacheEntry[*model.Team]{value: team, expires: c.clock().Add(metaCacheTTL)}
	c.mu.Unlock()
	return team, nil
}

// channel returns the channel with the given ID, from cache when fresh.
func (p *MattermostToolProvider) channel(ctx context.Context, client *model.Client4, userID, channelID string) (*model.Channel, error) {
	c := &p.cache
	key := cacheKey(userID, channelID)

	c.mu.Lock()
	channel, ok := lookup(c, c.channels, key)
	c.mu.Unlock()
	if ok {
		return channel, nil
	}

	channel, _, err := client.GetChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.channels == nil {
		c.channels = make(map[string]cacheEntry[*model.Channel])
	}
	c.channels[key] = cacheEntry[*model.Channel]{value: channel, expires: c.clock().Add(metaCacheTTL)}
	c.mu.Unlock()
	return channel, nil
}

// teamDisplayName resolves a team's display name for listings, "" when the
// channel has no team or the lookup fails.
func (p *MattermostToolProvider) teamDisplayName(ctx context.Context, client *model.Client4, userID, teamID string) string {
	if teamID == "" {
		return ""
	}
	team, err := p.team(ctx, client, userID, teamID)
	if err != nil {
		p.logger.Warn("failed to fetch team", "team_id", teamID, "error", err)
		return ""
	}
	return team.DisplayName
}
