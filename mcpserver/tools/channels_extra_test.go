// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelExtraToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_channel_stats", func() (string, error) {
			return provider.toolGetChannelStats(mcpCtx, GetChannelStatsArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_channel_member_counts empty", func() (string, error) {
			return provider.toolGetChannelMemberCounts(mcpCtx, GetChannelMemberCountsArgs{})
		}, "cannot be empty"},
		{"search_channels empty term", func() (string, error) {
			return provider.toolSearchChannels(mcpCtx, SearchChannelsArgs{Term: ""})
		}, "term cannot be empty"},
		{"list_team_channels bad", func() (string, error) {
			return provider.toolListTeamChannels(mcpCtx, ListTeamChannelsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"list_archived_channels bad", func() (string, error) {
			return provider.toolListArchivedChannels(mcpCtx, ListArchivedChannelsArgs{TeamID: "bad"})
		}, "must be a valid ID"},
		{"update_channel no fields", func() (string, error) {
			return provider.toolUpdateChannel(mcpCtx, UpdateChannelArgs{ChannelID: model.NewId()})
		}, "provide at least one"},
		{"archive_channel bad", func() (string, error) {
			return provider.toolArchiveChannel(mcpCtx, ArchiveChannelArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"restore_channel bad", func() (string, error) {
			return provider.toolRestoreChannel(mcpCtx, RestoreChannelArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"convert_channel_privacy bad privacy", func() (string, error) {
			return provider.toolConvertChannelPrivacy(mcpCtx, ConvertChannelPrivacyArgs{ChannelID: model.NewId(), Privacy: "X"})
		}, "invalid privacy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestToolGetChannelStats(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/stats", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelStats{ChannelId: channelID, MemberCount: 12, GuestCount: 1, PinnedPostCount: 3, FilesCount: 5})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetChannelStats(mcpCtx, GetChannelStatsArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Members: 12")
	assert.Contains(t, out, "Pinned posts: 3")
}

func TestToolUpdateChannel(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	var gotPatch model.ChannelPatch
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/patch", channelID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotPatch)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, DisplayName: *gotPatch.DisplayName})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	newName := "Renamed"
	out, err := provider.toolUpdateChannel(mcpCtx, UpdateChannelArgs{ChannelID: channelID, DisplayName: &newName})
	require.NoError(t, err)
	require.NotNil(t, gotPatch.DisplayName)
	assert.Equal(t, "Renamed", *gotPatch.DisplayName)
	assert.Contains(t, out, "Successfully updated channel")
}

func TestToolConvertChannelPrivacy(t *testing.T) {
	channelID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/privacy", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, DisplayName: "C", Type: model.ChannelTypePrivate})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolConvertChannelPrivacy(mcpCtx, ConvertChannelPrivacyArgs{ChannelID: channelID, Privacy: "P"})
	require.NoError(t, err)
	assert.Contains(t, out, "type P")
}

func TestToolGetChannelMemberCounts(t *testing.T) {
	c1 := model.NewId()
	c2 := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/stats/member_count", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{c1: 4, c2: 9})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetChannelMemberCounts(mcpCtx, GetChannelMemberCountsArgs{ChannelIDs: []string{c1, c2}})
	require.NoError(t, err)
	assert.Contains(t, out, fmt.Sprintf("%s: 4", c1))
	assert.Contains(t, out, fmt.Sprintf("%s: 9", c2))
}

func TestToolGetActiveChannels(t *testing.T) {
	meID := model.NewId()
	friendID := model.NewId()
	teamID := model.NewId()

	dmName := meID + "__" + friendID
	if friendID < meID {
		dmName = friendID + "__" + meID
	}

	channels := []*model.Channel{
		{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Town Square", TeamId: teamID, LastPostAt: 2000},
		{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: dmName, LastPostAt: 5000},
		{Id: model.NewId(), Type: model.ChannelTypePrivate, DisplayName: "Stale Private", TeamId: teamID, LastPostAt: 500},
		{Id: model.NewId(), Type: model.ChannelTypeGroup, DisplayName: "alice, bob", LastPostAt: 3000},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: meID, Username: "me"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/channels", meID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(channels)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", meID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Team{{Id: teamID, DisplayName: "Main Team"}})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, r *http.Request) {
		var ids []string
		_ = json.NewDecoder(r.Body).Decode(&ids)
		assert.Equal(t, []string{friendID}, ids)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: friendID, Username: "friend"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	t.Run("filters by since, sorts newest first, resolves DM username", func(t *testing.T) {
		out, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{Since: "1970-01-01T00:00:01Z"})
		require.NoError(t, err)
		assert.Contains(t, out, "showing 3 of 3")
		assert.Contains(t, out, "DM with @friend")
		assert.Contains(t, out, "Town Square")
		assert.Contains(t, out, "Team: Main Team")
		assert.NotContains(t, out, "Stale Private")
		dmIdx := strings.Index(out, "DM with @friend")
		groupIdx := strings.Index(out, "alice, bob")
		openIdx := strings.Index(out, "Town Square")
		assert.Less(t, dmIdx, groupIdx)
		assert.Less(t, groupIdx, openIdx)
	})

	t.Run("filters by channel type", func(t *testing.T) {
		out, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{Since: "1970-01-01T00:00:01Z", ChannelTypes: []string{"D"}})
		require.NoError(t, err)
		assert.Contains(t, out, "showing 1 of 1")
		assert.Contains(t, out, "DM with @friend")
		assert.NotContains(t, out, "Town Square")
	})

	t.Run("no matches", func(t *testing.T) {
		out, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{Since: "1970-01-01T00:00:10Z"})
		require.NoError(t, err)
		assert.Contains(t, out, "No channels with posts since")
	})

	t.Run("rejects invalid channel type", func(t *testing.T) {
		_, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{Since: "1970-01-01T00:00:01Z", ChannelTypes: []string{"X"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid channel type")
	})

	t.Run("rejects empty since", func(t *testing.T) {
		_, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "since cannot be empty")
	})

	t.Run("rejects malformed since", func(t *testing.T) {
		_, err := provider.toolGetActiveChannels(mcpCtx, GetActiveChannelsArgs{Since: "yesterday"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid timestamp format")
	})
}

// buildPostList makes a PostList with `count` posts in the given channel,
// with create timestamps startAt, startAt+1000, ... (ordered newest first, as the API returns).
func buildPostList(channelID, userID string, count int, startAt int64) *model.PostList {
	list := model.NewPostList()
	for i := count - 1; i >= 0; i-- {
		post := &model.Post{
			Id:        model.NewId(),
			ChannelId: channelID,
			UserId:    userID,
			Message:   fmt.Sprintf("msg-%d", i),
			CreateAt:  startAt + int64(i)*1000,
		}
		list.AddPost(post)
		list.AddOrder(post.Id)
	}
	return list
}

func TestToolReadChannelLargeWindows(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	const sinceMillis = int64(1_700_000_000_000)

	sincePosts := buildPostList(channelID, userID, 250, sinceMillis)

	pagedPosts := map[int]*model.PostList{
		0: buildPostList(channelID, userID, 200, sinceMillis+1_000_000),
		1: buildPostList(channelID, userID, 200, sinceMillis+2_000_000),
		2: buildPostList(channelID, userID, 50, sinceMillis+3_000_000),
	}
	var pagesRequested []int

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Type: model.ChannelTypeDirect})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("since") != "" {
			_ = json.NewEncoder(w).Encode(sincePosts)
			return
		}
		page := 0
		_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		assert.Equal(t, "200", r.URL.Query().Get("per_page"), "pages beyond the API cap must be requested in 200-post chunks")
		pagesRequested = append(pagesRequested, page)
		list := pagedPosts[page]
		if list == nil {
			list = model.NewPostList()
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: userID, Username: "someone"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}
	sinceISO := time.UnixMilli(sinceMillis).UTC().Format(time.RFC3339)

	t.Run("since fetches the full window beyond one API page", func(t *testing.T) {
		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, Limit: 500})
		require.NoError(t, err)
		assert.Contains(t, out, "Found 250 posts")
		assert.Contains(t, out, "msg-0")
		assert.Contains(t, out, "msg-249")
		assert.NotContains(t, out, "Showing the oldest")
	})

	t.Run("since over limit keeps oldest posts and explains how to continue", func(t *testing.T) {
		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, Limit: 100})
		require.NoError(t, err)
		assert.Contains(t, out, "Found 100 posts")
		assert.Contains(t, out, "msg-0")
		assert.Contains(t, out, "msg-99")
		assert.NotContains(t, out, "msg-100")
		assert.Contains(t, out, "Showing the oldest 100 of 250 matching posts")
		assert.Contains(t, out, "after=")
	})

	t.Run("limit above API page cap paginates internally", func(t *testing.T) {
		pagesRequested = nil
		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Limit: 500})
		require.NoError(t, err)
		assert.Contains(t, out, "Found 450 posts")
		assert.Equal(t, []int{0, 1, 2}, pagesRequested)
	})
}

func TestToolReadChannelFromFilter(t *testing.T) {
	channelID := model.NewId()
	targetID := model.NewId()
	otherID := model.NewId()
	const sinceMillis = int64(1_700_000_000_000)
	sinceISO := time.UnixMilli(sinceMillis).UTC().Format(time.RFC3339)

	// 6 posts: only two by the target user.
	mixed := model.NewPostList()
	for i, userID := range []string{otherID, targetID, otherID, otherID, targetID, otherID} {
		post := &model.Post{
			Id:        model.NewId(),
			ChannelId: channelID,
			UserId:    userID,
			Message:   fmt.Sprintf("msg-%d", i),
			CreateAt:  sinceMillis + int64(i)*1000,
		}
		mixed.AddPost(post)
		mixed.AddOrder(post.Id)
	}

	newMux := func(list *model.PostList, knownUsername string) *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Busy"})
		})
		mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
		})
		mux.HandleFunc(fmt.Sprintf("/api/v4/users/username/%s", knownUsername), func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&model.User{Id: targetID, Username: knownUsername})
		})
		mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]*model.User{{Id: targetID, Username: knownUsername}, {Id: otherID, Username: "someone.else"}})
		})
		return mux
	}

	t.Run("returns only that author's posts", func(t *testing.T) {
		ts := httptest.NewServer(newMux(mixed, "target.user"))
		defer ts.Close()
		provider := newTestProvider(t, ts.URL)
		mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, From: "@target.user", Limit: 500})
		require.NoError(t, err)
		assert.Contains(t, out, "Found 2 posts by @target.user (of 6 posts scanned)")
		assert.Contains(t, out, "msg-1")
		assert.Contains(t, out, "msg-4")
		assert.NotContains(t, out, "msg-0")
		assert.NotContains(t, out, "msg-5")
	})

	t.Run("says plainly when that author posted nothing", func(t *testing.T) {
		onlyOthers := model.NewPostList()
		post := &model.Post{Id: model.NewId(), ChannelId: channelID, UserId: otherID, Message: "bot noise", CreateAt: sinceMillis + 1}
		onlyOthers.AddPost(post)
		onlyOthers.AddOrder(post.Id)

		ts := httptest.NewServer(newMux(onlyOthers, "target.user"))
		defer ts.Close()
		provider := newTestProvider(t, ts.URL)
		mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, From: "target.user"})
		require.NoError(t, err)
		assert.Contains(t, out, "no posts by @target.user among the 1 posts")
		assert.NotContains(t, out, "server cap")
	})

	t.Run("unknown username errors instead of reading as empty", func(t *testing.T) {
		ts := httptest.NewServer(newMux(mixed, "target.user"))
		defer ts.Close()
		provider := newTestProvider(t, ts.URL)
		mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

		_, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, From: "ghost.user"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `resolving from="ghost.user"`)
	})
}

func TestToolReadChannelServerCapHonesty(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	targetID := model.NewId()
	const sinceMillis = int64(1_700_000_000_000)
	sinceISO := time.UnixMilli(sinceMillis).UTC().Format(time.RFC3339)

	// A window at the server cap: the since endpoint returned an arbitrary
	// part of it, and every page read back from the newest post is still
	// inside the window.
	capped := buildPostList(channelID, userID, sincePostsServerCap, sinceMillis)
	const perPage = 200
	pageStart := func(page int) int64 {
		return sinceMillis + int64(sincePostsServerCap/perPage-page)*1_000_000
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s", channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Firehose"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("since") != "" {
			_ = json.NewEncoder(w).Encode(capped)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		_ = json.NewEncoder(w).Encode(buildPostList(channelID, userID, perPage, pageStart(page)))
	})
	mux.HandleFunc("/api/v4/users/username/absent.user", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.User{Id: targetID, Username: "absent.user"})
	})
	mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.User{{Id: userID, Username: "chatty"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	t.Run("admits the window was only partially scanned", func(t *testing.T) {
		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, Limit: 10})
		require.NoError(t, err)
		assert.Contains(t, out, fmt.Sprintf("more posts than one call scans (%d)", sincePostsServerCap))
		// The scanned part starts at the oldest post of the last page read.
		assert.Contains(t, out, "posts before "+time.UnixMilli(pageStart(sincePostsServerCap/perPage-1)).UTC().Format(time.RFC3339)+" were not read")
		assert.Contains(t, out, "To continue, call read_channel with after=")
		// The old wording claimed an exact total; it must not come back.
		assert.NotContains(t, out, fmt.Sprintf("of %d matching posts", sincePostsServerCap))
	})

	t.Run("a from-filter miss is reported as inconclusive, not as absence", func(t *testing.T) {
		out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, From: "absent.user"})
		require.NoError(t, err)
		assert.Contains(t, out, "no posts by @absent.user")
		assert.Contains(t, out, "only partially scanned")
		assert.Contains(t, out, "narrow the timeframe")
	})
}

func TestToolGetActiveChannelsLastPost(t *testing.T) {
	meID := model.NewId()
	friendID := model.NewId()
	botID := model.NewId()

	dmWith := func(other string) string {
		if other < meID {
			return other + "__" + meID
		}
		return meID + "__" + other
	}
	friendDM := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: dmWith(friendID), LastPostAt: 5000}
	answeredDM := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: dmWith(model.NewId()), LastPostAt: 4000}
	botDM := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: dmWith(botID), LastPostAt: 3000}
	unreadable := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeGroup, DisplayName: "broken", LastPostAt: 2000}

	// Newest first, as the API orders them; the friend's DM ends with a
	// system message that must be skipped.
	lastPosts := map[string][]*model.Post{
		friendDM.Id: {
			{Id: model.NewId(), UserId: friendID, Type: model.PostTypeJoinChannel, Message: "joined"},
			{Id: model.NewId(), UserId: friendID, Message: "can you   look\nat this?"},
		},
		answeredDM.Id: {{Id: model.NewId(), UserId: meID, Message: "done"}},
		botDM.Id:      {{Id: model.NewId(), UserId: botID, Message: "build failed"}},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/channels", meID), func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, []*model.Channel{friendDM, answeredDM, botDM, unreadable})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams", meID), func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, []*model.Team{})
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{
		{Id: meID, Username: "me"},
		{Id: friendID, Username: "friend"},
		{Id: botID, Username: "ci", IsBot: true},
	}))
	mux.HandleFunc("/api/v4/channels/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v4/channels/"), "/posts")
		posts, ok := lastPosts[channelID]
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		list := model.NewPostList()
		for _, post := range posts {
			post.ChannelId = channelID
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, list)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: meID}
	since := "1970-01-01T00:00:01Z"

	tests := []struct {
		name    string
		args    GetActiveChannelsArgs
		want    []string
		notWant []string
	}{
		{
			name:    "with_last_post shows author and one-line preview, skipping system messages",
			args:    GetActiveChannelsArgs{Since: since, WithLastPost: true},
			want:    []string{"showing 4 of 4", "last: @friend: can you look at this?", "last: @me: done", "last: @ci: build failed"},
			notWant: []string{"joined"},
		},
		{
			name:    "awaiting_reply keeps only conversations last written by another person",
			args:    GetActiveChannelsArgs{Since: since, AwaitingReply: true},
			want:    []string{"showing 1 of 1", "DM with @friend", "last: @friend"},
			notWant: []string{"done", "build failed", "broken"},
		},
		{
			name:    "without the flags no posts are fetched",
			args:    GetActiveChannelsArgs{Since: since},
			want:    []string{"showing 4 of 4"},
			notWant: []string{"last:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolGetActiveChannels(mcpCtx, tt.args)
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

func TestReadChannelNamesDirectMessage(t *testing.T) {
	meID := model.NewId()
	friend := &model.User{Id: model.NewId(), Username: "friend"}
	name := meID + "__" + friend.Id
	if friend.Id < meID {
		name = friend.Id + "__" + meID
	}
	dm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: name}
	post := &model.Post{Id: model.NewId(), ChannelId: dm.Id, UserId: meID, Message: "hello", CreateAt: 1000}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/"+dm.Id, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, dm)
	})
	mux.HandleFunc("/api/v4/channels/"+dm.Id+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		list := model.NewPostList()
		list.AddPost(post)
		list.AddOrder(post.Id)
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{{Id: meID, Username: "me"}, friend}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: meID}
	out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: dm.Id})
	require.NoError(t, err)
	assert.Contains(t, out, "Channel: DM with @friend")
	assert.Contains(t, out, "by me")
}

func TestToolReadChannels(t *testing.T) {
	author := &model.User{Id: model.NewId(), Username: "alice"}
	teamID := model.NewId()
	readable := []*model.Channel{
		{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Ops", TeamId: teamID},
		{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Dev", TeamId: teamID},
	}
	forbidden := model.NewId()

	mux := http.NewServeMux()
	for _, ch := range readable {
		mux.HandleFunc("/api/v4/channels/"+ch.Id, func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, ch)
		})
		mux.HandleFunc("/api/v4/channels/"+ch.Id+"/posts", func(w http.ResponseWriter, _ *http.Request) {
			post := &model.Post{Id: model.NewId(), ChannelId: ch.Id, UserId: author.Id, Message: "hello from " + ch.DisplayName, CreateAt: 1000}
			list := model.NewPostList()
			list.AddPost(post)
			list.AddOrder(post.Id)
			writeTestJSON(w, list)
		})
	}
	mux.HandleFunc("/api/v4/channels/"+forbidden, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"no access"}`, http.StatusForbidden)
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{author}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		ids     []string
		want    []string
		wantErr string
	}{
		{
			name: "reads each channel in order and reports per-channel errors",
			ids:  []string{readable[0].Id, forbidden, readable[1].Id},
			want: []string{"=== Channel 1 of 3", "hello from Ops", "=== Channel 2 of 3", "Error: error fetching channel", "=== Channel 3 of 3", "hello from Dev"},
		},
		{name: "rejects an empty list", ids: nil, wantErr: "cannot be empty"},
		{name: "rejects malformed IDs", ids: []string{"bad"}, wantErr: "valid ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolReadChannels(mcpCtx, ReadChannelsArgs{ChannelIDs: tt.ids})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			last := -1
			for _, want := range tt.want {
				idx := strings.Index(out, want)
				assert.Greater(t, idx, last, "%q missing or out of order", want)
				last = idx
			}
		})
	}
}

func TestGetChannelInfoIgnoresEmojiAndMatchesPartially(t *testing.T) {
	meID := model.NewId()
	teamID := model.NewId()
	infra := &model.Channel{Id: model.NewId(), TeamId: teamID, Type: model.ChannelTypePrivate, Name: "platform-infrastructure", DisplayName: "🛠️ Platform Infrastructure"}
	alerts := &model.Channel{Id: model.NewId(), TeamId: teamID, Type: model.ChannelTypePrivate, Name: "infra-alerts", DisplayName: "Infra Alerts"}
	all := []*model.Channel{infra, alerts}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/search", func(w http.ResponseWriter, _ *http.Request) {
		// The server's search is fuzzy; return everything and let the tool match.
		var list model.ChannelListWithTeamData
		for _, ch := range all {
			list = append(list, &model.ChannelWithTeamData{Channel: *ch, TeamDisplayName: "Main"})
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/channels", meID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, all)
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
	})
	mux.HandleFunc("/api/v4/channels/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stats") {
			writeTestJSON(w, &model.ChannelStats{MemberCount: 3})
			return
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: meID}

	tests := []struct {
		name    string
		args    GetChannelInfoArgs
		want    []string
		notWant []string
	}{
		{"exact name without the emoji, across teams", GetChannelInfoArgs{ChannelName: "platform infrastructure"}, []string{"Platform Infrastructure", infra.Id}, []string{"Infra Alerts"}},
		{"exact name without the emoji, within a team", GetChannelInfoArgs{ChannelName: "Platform Infrastructure", TeamID: teamID}, []string{infra.Id}, []string{"Infra Alerts"}},
		{"partial name across teams", GetChannelInfoArgs{ChannelName: "infrastructure"}, []string{infra.Id}, []string{alerts.Id}},
		{"partial name matching several channels lists all", GetChannelInfoArgs{ChannelName: "infra"}, []string{"Found 2 channels", infra.Id, alerts.Id}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolGetChannelInfo(mcpCtx, tt.args)
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

// The since endpoint reports deleted posts with their text blanked, and at its
// cap returns an unordered part of the window; read_channel shows neither.
func TestToolReadChannelSinceSkipsDeletedAndReadsOrderedWindow(t *testing.T) {
	userID := model.NewId()
	const sinceMillis = int64(1_700_000_000_000)
	sinceISO := time.UnixMilli(sinceMillis).UTC().Format(time.RFC3339)

	tests := []struct {
		name      string
		sincePart func(channelID string) *model.PostList
		paged     func(channelID string) *model.PostList
		want      []string
		notWant   []string
	}{
		{
			name: "deleted posts are left out",
			sincePart: func(channelID string) *model.PostList {
				list := buildPostList(channelID, userID, 3, sinceMillis)
				list.Posts[list.Order[0]].DeleteAt = sinceMillis + 5000
				list.Posts[list.Order[0]].Message = ""
				return list
			},
			want:    []string{"Found 2 posts", "msg-0", "msg-1"},
			notWant: []string{"Post 3"},
		},
		{
			name: "a window at the since cap is read from ordered pages",
			sincePart: func(channelID string) *model.PostList {
				return buildPostList(channelID, userID, sincePostsServerCap, sinceMillis)
			},
			paged: func(channelID string) *model.PostList {
				// Two posts inside the window, one before it.
				return buildPostList(channelID, userID, 3, sinceMillis-1000)
			},
			want:    []string{"Found 2 posts", "msg-1", "msg-2"},
			notWant: []string{"msg-0", "were not read"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelID := model.NewId()
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Town"})
			})
			mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("since") != "" {
					writeTestJSON(w, tt.sincePart(channelID))
					return
				}
				writeTestJSON(w, tt.paged(channelID))
			})
			mux.HandleFunc("/api/v4/users/ids", func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, []*model.User{{Id: userID, Username: "chatty"}})
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			provider := newTestProvider(t, ts.URL)
			mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}
			out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, Since: sinceISO, Limit: 50})
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

// Without since, from filters a single page: a miss there is not "did not post".
func TestToolReadChannelFromOnOnePageIsNotAbsence(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Town"})
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/%s/posts", channelID), func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, buildPostList(channelID, userID, 5, 1_700_000_000_000))
	})
	mux.HandleFunc("/api/v4/users/username/absent.user", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.User{Id: model.NewId(), Username: "absent.user"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}
	out, err := provider.toolReadChannel(mcpCtx, ReadChannelArgs{ChannelID: channelID, From: "absent.user", Limit: 5})
	require.NoError(t, err)
	assert.Contains(t, out, "among the 5 posts on this page")
	assert.Contains(t, out, "does not cover the rest of the channel")
}

// Display names that differ only in emoji match the same query; the tool must
// not pick one of them silently.
func TestGetChannelInfoAmbiguousNamesInTeam(t *testing.T) {
	meID := model.NewId()
	teamID := model.NewId()
	red := &model.Channel{Id: model.NewId(), TeamId: teamID, Type: model.ChannelTypeOpen, Name: "alerts-prod", DisplayName: "🔴 Alerts"}
	green := &model.Channel{Id: model.NewId(), TeamId: teamID, Type: model.ChannelTypeOpen, Name: "alerts-test", DisplayName: "🟢 Alerts"}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/channels", meID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, []*model.Channel{red, green})
	})
	mux.HandleFunc("/api/v4/teams/"+teamID, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, &model.Team{Id: teamID, DisplayName: "Main"})
	})
	mux.HandleFunc("/api/v4/channels/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stats") {
			writeTestJSON(w, &model.ChannelStats{MemberCount: 3})
			return
		}
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: meID}

	tests := []struct {
		name    string
		query   string
		want    []string
		notWant []string
	}{
		{"a name matching both lists both", "Alerts", []string{"Found 2 channels", red.Id, green.Id}, nil},
		{"the literal display name picks its channel", "🟢 Alerts", []string{green.Id}, []string{red.Id}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolGetChannelInfo(mcpCtx, GetChannelInfoArgs{ChannelName: tt.query, TeamID: teamID})
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
