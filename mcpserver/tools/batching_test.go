// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requestCounter records how many requests hit each API path.
type requestCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *requestCounter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		if c.counts == nil {
			c.counts = map[string]int{}
		}
		c.counts[r.URL.Path]++
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (c *requestCounter) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[path]
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// usersByIDsHandler answers POST /users/ids with the known users among the
// requested IDs.
func usersByIDsHandler(t *testing.T, known []*model.User) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ids []string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&ids))
		var users []*model.User
		for _, id := range ids {
			for _, u := range known {
				if u.Id == id {
					users = append(users, u)
				}
			}
		}
		writeTestJSON(w, users)
	}
}

// Listing members, a thread, or search results must resolve every author in
// one request rather than one per user: each per-user round trip is paid by
// the client, not by the server.
func TestAuthorsResolvedInOneRequest(t *testing.T) {
	channelID := model.NewId()
	teamID := model.NewId()
	rootID := model.NewId()
	users := []*model.User{
		{Id: model.NewId(), Username: "alice"},
		{Id: model.NewId(), Username: "bob"},
		{Id: model.NewId(), Username: "carol"},
	}
	posts := make([]*model.Post, 0, 6)
	for i := range 6 {
		posts = append(posts, &model.Post{
			Id:        model.NewId(),
			ChannelId: channelID,
			UserId:    users[i%len(users)].Id,
			Message:   fmt.Sprintf("message %d", i),
			CreateAt:  int64(1000 + i),
		})
	}
	posts[0].Id = rootID

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, users))
	mux.HandleFunc("/api/v4/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/ids" {
			return
		}
		t.Errorf("per-user lookup %s must not happen", r.URL.Path)
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Channel{Id: channelID, TeamId: teamID, DisplayName: "Town Square", Type: model.ChannelTypeOpen})
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
	})
	mux.HandleFunc("/api/v4/channels/"+channelID+"/members", func(w http.ResponseWriter, _ *http.Request) {
		members := make(model.ChannelMembers, 0, len(users))
		for _, u := range users {
			members = append(members, model.ChannelMember{ChannelId: channelID, UserId: u.Id, SchemeUser: true})
		}
		writeTestJSON(w, members)
	})
	mux.HandleFunc("/api/v4/posts/"+rootID+"/thread", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		for _, post := range posts {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/teams/"+teamID+"/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		for _, post := range posts {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, &model.PostSearchResults{PostList: list})
	})
	mux.HandleFunc("/api/v4/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		for _, post := range posts {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, &model.PostSearchResults{PostList: list})
	})

	tests := []struct {
		name string
		call func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error)
		want []string
	}{
		{
			name: "get_channel_members",
			call: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolGetChannelMembers(mcpCtx, GetChannelMembersArgs{ChannelID: channelID})
			},
			want: []string{"alice", "bob", "carol"},
		},
		{
			name: "read_post thread",
			call: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolReadPost(mcpCtx, ReadPostArgs{PostID: rootID})
			},
			want: []string{"alice", "bob", "carol", "Town Square", "Main", "Thread with 6 posts"},
		},
		{
			name: "search_posts keyword",
			call: func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
				return p.toolCombinedSearch(mcpCtx, CombinedSearchArgs{Query: "message", TeamID: teamID})
			},
			want: []string{"alice", "bob", "carol", "Town Square", "Main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &requestCounter{}
			ts := httptest.NewServer(counter.wrap(mux))
			defer ts.Close()

			p := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), AccessMode: AccessModeLocal, UserID: model.NewId()}

			out, err := tt.call(p, mcpCtx)
			require.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}
			assert.Equal(t, 1, counter.count("/api/v4/users/ids"), "authors must be fetched in one request")
			assert.LessOrEqual(t, counter.count("/api/v4/teams/"+teamID), 1, "a team is fetched at most once")
		})
	}
}

// A team or channel looked up once is served from memory for later calls by
// the same user, and never to a different user.
func TestMetaCacheServesRepeatLookups(t *testing.T) {
	teamID := model.NewId()
	channelID := model.NewId()

	tests := []struct {
		name         string
		secondUserID func(first string) string
		advance      time.Duration
		wantRequests int
	}{
		{"same user within TTL", func(first string) string { return first }, 0, 1},
		{"same user after TTL", func(first string) string { return first }, metaCacheTTL + time.Second, 2},
		{"different user", func(string) string { return model.NewId() }, 0, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &requestCounter{}
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
			})
			mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, &model.Channel{Id: channelID, DisplayName: "Town Square"})
			})
			ts := httptest.NewServer(counter.wrap(mux))
			defer ts.Close()

			p := newTestProvider(t, ts.URL)
			now := time.Now()
			p.cache.now = func() time.Time { return now }
			client := newTestClient(ts.URL)
			firstUser := model.NewId()
			secondUser := tt.secondUserID(firstUser)

			team, err := p.team(t.Context(), client, firstUser, teamID)
			require.NoError(t, err)
			channel, err := p.channel(t.Context(), client, firstUser, channelID)
			require.NoError(t, err)

			now = now.Add(tt.advance)

			teamAgain, err := p.team(t.Context(), client, secondUser, teamID)
			require.NoError(t, err)
			channelAgain, err := p.channel(t.Context(), client, secondUser, channelID)
			require.NoError(t, err)

			assert.Equal(t, team.DisplayName, teamAgain.DisplayName)
			assert.Equal(t, channel.DisplayName, channelAgain.DisplayName)
			assert.Equal(t, tt.wantRequests, counter.count("/api/v4/teams/"+teamID))
			assert.Equal(t, tt.wantRequests, counter.count("/api/v4/channels/"+channelID))
		})
	}
}

// A failed lookup is not cached: the next call asks the server again.
func TestMetaCacheDoesNotCacheFailures(t *testing.T) {
	teamID := model.NewId()
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	userID := model.NewId()

	_, err := p.team(t.Context(), client, userID, teamID)
	require.Error(t, err)
	team, err := p.team(t.Context(), client, userID, teamID)
	require.NoError(t, err)
	assert.Equal(t, "Main", team.DisplayName)
	assert.Equal(t, int32(2), calls.Load())
}

// Once the plugin answers that semantic search is not available, the service
// stays disabled for the cooldown instead of asking again on every search.
func TestHTTPSemanticSearchCooldown(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		wantEnabled   bool
		enabledAfter  time.Duration
		wantReenabled bool
	}{
		{"503 disables for the cooldown", http.StatusServiceUnavailable, false, semanticUnavailableCooldown + time.Second, true},
		{"404 disables for the cooldown", http.StatusNotFound, false, semanticUnavailableCooldown + time.Second, true},
		{"500 keeps the service enabled", http.StatusInternalServerError, true, 0, true},
		{"401 keeps the service enabled", http.StatusUnauthorized, true, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "semantic search is not available"})
			}))
			defer ts.Close()

			svc := NewHTTPSemanticSearchService(ts.URL)
			now := time.Now()
			svc.now = func() time.Time { return now }

			require.True(t, svc.Enabled())
			_, err := svc.Search(t.Context(), "query", search.Options{})
			require.Error(t, err)
			assert.Equal(t, tt.wantEnabled, svc.Enabled())

			now = now.Add(tt.enabledAfter)
			assert.Equal(t, tt.wantReenabled, svc.Enabled())
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

// forEachParallel visits every index exactly once and never exceeds the
// configured parallelism.
func TestForEachParallel(t *testing.T) {
	const n = 50
	var visited [n]atomic.Int32
	var inFlight, peak atomic.Int32

	forEachParallel(n, func(i int) {
		cur := inFlight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		visited[i].Add(1)
		inFlight.Add(-1)
	})

	for i := range n {
		assert.Equal(t, int32(1), visited[i].Load(), "index %d", i)
	}
	assert.LessOrEqual(t, peak.Load(), int32(apiParallelism))
	assert.Greater(t, peak.Load(), int32(1), "work must actually run in parallel")
}
