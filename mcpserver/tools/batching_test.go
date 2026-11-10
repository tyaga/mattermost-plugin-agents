// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// The search endpoint answers with its first 60 hits unless asked for a
// page; offsets past that must still reach the right results.
func TestKeywordSearchPagesPastDefault(t *testing.T) {
	const total = 150
	channelID := model.NewId()
	author := &model.User{Id: model.NewId(), Username: "alice"}
	hits := make([]*model.Post, total)
	for i := range hits {
		// Newest first, as the server orders search hits.
		hits[i] = &model.Post{Id: model.NewId(), ChannelId: channelID, UserId: author.Id, Message: fmt.Sprintf("hit-%03d", i), CreateAt: int64(10_000 - i)}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/search", func(w http.ResponseWriter, r *http.Request) {
		var params model.SearchParameter
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))
		page, perPage := 0, 60
		if params.Page != nil {
			page = *params.Page
		}
		if params.PerPage != nil {
			perPage = *params.PerPage
		}
		list := model.NewPostList()
		for i := page * perPage; i < min((page+1)*perPage, total); i++ {
			list.AddPost(hits[i])
			list.AddOrder(hits[i].Id)
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{author}))
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Channel{Id: channelID, DisplayName: "Ops", Type: model.ChannelTypeOpen})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), AccessMode: AccessModeLocal, UserID: model.NewId()}

	tests := []struct {
		name          string
		offset, limit int
		first, last   int // expected hit indexes; -1 for no results
	}{
		{"first page", 0, 10, 0, 9},
		{"window past the default 60", 70, 10, 70, 79},
		{"window spanning two pages", 95, 10, 95, 104},
		{"tail shorter than limit", 145, 10, 145, 149},
		{"offset past the end", 150, 10, -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := p.toolCombinedSearch(mcpCtx, CombinedSearchArgs{Query: "hit", KeywordOffset: tt.offset, KeywordLimit: tt.limit})
			require.NoError(t, err)
			if tt.first < 0 {
				assert.NotContains(t, out, "hit-")
				return
			}
			assert.Contains(t, out, fmt.Sprintf("hit-%03d", tt.first))
			assert.Contains(t, out, fmt.Sprintf("hit-%03d", tt.last))
			if tt.first > 0 {
				assert.NotContains(t, out, fmt.Sprintf("hit-%03d", tt.first-1))
			}
			assert.NotContains(t, out, fmt.Sprintf("hit-%03d", tt.last+1))
		})
	}
}

func TestReadPostThreadLimit(t *testing.T) {
	channelID := model.NewId()
	author := &model.User{Id: model.NewId(), Username: "alice"}
	thread := make([]*model.Post, 10)
	for i := range thread {
		thread[i] = &model.Post{Id: model.NewId(), ChannelId: channelID, UserId: author.Id, Message: fmt.Sprintf("msg-%02d", i+1), CreateAt: int64(1000 + i)}
		if i > 0 {
			thread[i].RootId = thread[0].Id
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/"+thread[0].Id+"/thread", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		for _, post := range thread {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{author}))
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Channel{Id: channelID, DisplayName: "Ops", Type: model.ChannelTypeOpen})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), UserID: model.NewId()}

	tests := []struct {
		name    string
		limit   int
		want    []string
		notWant []string
	}{
		{"whole thread under the limit", 20, []string{"Thread with 10 posts", "msg-01", "msg-05", "msg-10"}, []string{"skipped"}},
		{"root plus latest replies", 4, []string{"Thread with 10 posts", "msg-01", "posts 2-7 of 10 skipped", "**Post 8**", "msg-08", "msg-10"}, []string{"msg-07", "msg-02"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := p.toolReadPost(mcpCtx, ReadPostArgs{PostID: thread[0].Id, Limit: tt.limit})
			require.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, out, notWant)
			}
		})
	}
}

// Thread and emoji listings resolve their authors in one request.
func TestListingAuthorsBatched(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	authors := []*model.User{{Id: model.NewId(), Username: "alice"}, {Id: model.NewId(), Username: "bob"}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, authors))
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/threads", userID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		threads := &model.Threads{Total: 3}
		for i, author := range []*model.User{authors[0], authors[1], authors[0]} {
			threads.Threads = append(threads.Threads, &model.ThreadResponse{
				PostId: model.NewId(),
				Post:   &model.Post{Id: model.NewId(), UserId: author.Id, Message: fmt.Sprintf("thread %d", i)},
			})
		}
		writeTestJSON(w, threads)
	})
	mux.HandleFunc("/api/v4/emoji", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, []*model.Emoji{
			{Id: model.NewId(), Name: "party", CreatorId: authors[0].Id},
			{Id: model.NewId(), Name: "ship", CreatorId: authors[1].Id},
		})
	})
	mux.HandleFunc("/api/v4/users/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("per-user lookup %s must not happen", r.URL.Path)
		http.NotFound(w, r)
	})

	tests := []struct {
		name string
		call func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error)
	}{
		{"get_threads", func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
			return p.toolGetThreads(mcpCtx, GetThreadsArgs{TeamID: teamID})
		}},
		{"list_custom_emoji", func(p *MattermostToolProvider, mcpCtx *MCPToolContext) (string, error) {
			return p.toolListCustomEmoji(mcpCtx, ListCustomEmojiArgs{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &requestCounter{}
			ts := httptest.NewServer(counter.wrap(mux))
			defer ts.Close()

			p := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), UserID: userID}
			out, err := tt.call(p, mcpCtx)
			require.NoError(t, err)
			assert.Contains(t, out, "alice")
			assert.Contains(t, out, "bob")
			assert.Equal(t, 1, counter.count("/api/v4/users/ids"))
		})
	}
}

func TestToolGroupFilter(t *testing.T) {
	tests := []struct {
		name     string
		groups   []string
		names    []string
		readOnly bool
		want     []string
		notWant  []string
	}{
		{"default registers everything", nil, nil, false, []string{"read_channel", "create_post", "search_posts", "get_me"}, nil},
		{"groups limit the catalog", []string{"channels", "search"}, nil, false, []string{"read_channel", "get_active_channels", "search_posts", "create_channel"}, []string{"create_post", "get_me"}},
		{"read-only drops state-changing tools", []string{"channels", "posts"}, nil, true, []string{"read_channel", "read_post"}, []string{"create_channel", "create_post", "dm"}},
		{"names keep only the listed tools", nil, []string{"read_channel", "dm"}, false, []string{"read_channel", "dm"}, []string{"read_post", "create_post", "get_active_channels"}},
		{"names combine with read-only", nil, []string{"read_channel", "dm"}, true, []string{"read_channel"}, []string{"dm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{accessMode: AccessModeLocal, toolGroupFilter: tt.groups, toolNameFilter: tt.names, readOnlyTools: tt.readOnly, logger: &testLogger{t: t}}
			names := p.ToolNames()
			for _, want := range tt.want {
				assert.Contains(t, names, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, names, notWant)
			}
		})
	}
	assert.Contains(t, ToolGroupNames(), "channels")
	assert.Contains(t, ToolGroupNames(), "dev")
	assert.Contains(t, AllToolNames(), "read_channel")
	assert.Contains(t, AllToolNames(), "create_user")
}

// Post listings carry a link that opens each post in the client, built from
// the external site URL and the team-independent _redirect route.
func TestPostListingsIncludePermalinks(t *testing.T) {
	channelID := model.NewId()
	author := &model.User{Id: model.NewId(), Username: "alice"}
	post := &model.Post{Id: model.NewId(), ChannelId: channelID, UserId: author.Id, Message: "hello", CreateAt: 1000}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Channel{Id: channelID, DisplayName: "Ops", Type: model.ChannelTypeOpen})
	})
	mux.HandleFunc("/api/v4/channels/"+channelID+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		list.AddPost(post)
		list.AddOrder(post.Id)
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{author}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	want := "https://chat.example.com/_redirect/pl/" + post.Id
	tests := []struct {
		name     string
		siteURL  string
		compact  bool
		want     string
		wantNone bool
	}{
		{name: "labeled layout", siteURL: "https://chat.example.com/", want: "Link: " + want},
		{name: "compact layout", siteURL: "https://chat.example.com", compact: true, want: "link=" + want},
		{name: "no site URL, no link", wantNone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t, ts.URL)
			p.siteURL = strings.TrimRight(tt.siteURL, "/")
			p.compactOutput = tt.compact
			mcpCtx := &MCPToolContext{Ctx: t.Context(), Client: newTestClient(ts.URL), UserID: model.NewId()}
			out, err := p.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID})
			require.NoError(t, err)
			if tt.wantNone {
				assert.NotContains(t, out, "_redirect/pl/")
				return
			}
			assert.Contains(t, out, tt.want)
		})
	}
}

func TestValidateDisabledToolParams(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		wantErr string
	}{
		{"optional parameters of known tools", []string{"read_channel.from", "search_posts.in"}, ""},
		{"nothing to disable", nil, ""},
		{"entry without a parameter", []string{"read_channel"}, "want tool.param"},
		{"unknown tool", []string{"read_everything.from"}, `unknown tool "read_everything"`},
		{"unknown parameter", []string{"read_channel.author"}, `has no parameter "author"`},
		{"required parameter", []string{"read_channel.channel_id"}, "is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDisabledToolParams(tt.entries)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
