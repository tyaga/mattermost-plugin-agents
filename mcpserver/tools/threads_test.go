// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreadToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context(), UserID: model.NewId()}

	tests := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"get_threads bad team", func() (string, error) { return provider.toolGetThreads(mcpCtx, GetThreadsArgs{TeamID: "bad"}) }, "must be a valid ID"},
		{"get_channel_unread bad", func() (string, error) {
			return provider.toolGetChannelUnread(mcpCtx, GetChannelUnreadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"get_posts_around_unread bad", func() (string, error) {
			return provider.toolGetPostsAroundUnread(mcpCtx, GetPostsAroundUnreadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"mark_channel_read bad", func() (string, error) {
			return provider.toolMarkChannelRead(mcpCtx, MarkChannelReadArgs{ChannelID: "bad"})
		}, "must be a valid ID"},
		{"mark_channels_viewed empty", func() (string, error) {
			return provider.toolMarkChannelsViewed(mcpCtx, MarkChannelsViewedArgs{})
		}, "cannot be empty"},
		{"mark_post_unread bad", func() (string, error) {
			return provider.toolMarkPostUnread(mcpCtx, MarkPostUnreadArgs{PostID: "bad"})
		}, "must be a valid ID"},
		{"set_thread_follow bad thread", func() (string, error) {
			return provider.toolSetThreadFollow(mcpCtx, SetThreadFollowArgs{TeamID: model.NewId(), ThreadID: "bad"})
		}, "must be a valid ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestMentionKeywords(t *testing.T) {
	user := &model.User{Username: "john", NotifyProps: model.StringMap{"mention_keys": "john,@john,jb, "}}
	got := mentionKeywords(user)
	// @john (username) deduped with the mention_keys @john; jb added; blanks dropped.
	assert.Contains(t, got, "@john")
	assert.Contains(t, got, "john")
	assert.Contains(t, got, "jb")
	for _, k := range got {
		assert.NotEmpty(t, k)
	}
}

func TestToolGetThreads(t *testing.T) {
	teamID := model.NewId()
	userID := model.NewId()
	authorID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/teams/%s/threads", userID, teamID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Threads{
			Total:               1,
			TotalUnreadThreads:  1,
			TotalUnreadMentions: 2,
			Threads: []*model.ThreadResponse{{
				PostId:        "root123",
				ReplyCount:    3,
				UnreadReplies: 1,
				LastReplyAt:   1700000000000,
				Post:          &model.Post{Id: "root123", UserId: authorID, Message: "thread root"},
			}},
		})
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{{Id: authorID, Username: "starter"}}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetThreads(mcpCtx, GetThreadsArgs{TeamID: teamID})
	require.NoError(t, err)
	assert.Contains(t, out, "Root Post ID: root123")
	assert.Contains(t, out, "thread root")
	assert.Contains(t, out, "starter")
}

func TestToolGetChannelUnread(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	teamID := model.NewId()

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/users/%s/channels/%s/unread", userID, channelID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelUnread{ChannelId: channelID, TeamId: teamID, MsgCount: 7, MentionCount: 2})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolGetChannelUnread(mcpCtx, GetChannelUnreadArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Contains(t, out, "Unread messages: 7")
	assert.Contains(t, out, "Mentions: 2")
}

func TestToolMarkChannelRead(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()

	mux := http.NewServeMux()
	var gotView model.ChannelView
	mux.HandleFunc(fmt.Sprintf("/api/v4/channels/members/%s/view", userID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotView)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.ChannelViewResponse{Status: "OK"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: userID}

	out, err := provider.toolMarkChannelRead(mcpCtx, MarkChannelReadArgs{ChannelID: channelID})
	require.NoError(t, err)
	assert.Equal(t, channelID, gotView.ChannelId)
	assert.Contains(t, out, "Successfully marked channel")
}

func TestToolGetMentions(t *testing.T) {
	me := &model.User{Id: model.NewId(), Username: "me", NotifyProps: model.StringMap{"mention_keys": "deploy"}}
	alice := &model.User{Id: model.NewId(), Username: "alice"}
	channel := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen, DisplayName: "Ops"}

	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	// answered: alice mentions me in a thread I replied to afterwards.
	answered := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: alice.Id, Message: "@me look", CreateAt: base + 3000}
	myReply := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: me.Id, RootId: answered.Id, Message: "on it", CreateAt: base + 4000}
	// pending: a reply mentioning me in a thread where I only posted before it.
	root := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: me.Id, Message: "plan", CreateAt: base + 1000}
	pending := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: alice.Id, RootId: root.Id, Message: "deploy now @me?", CreateAt: base + 5000}
	old := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: alice.Id, Message: "@me ancient", CreateAt: base - 48*3600*1000}
	own := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: me.Id, Message: "I said deploy", CreateAt: base + 6000}

	threads := map[string][]*model.Post{
		answered.Id: {answered, myReply},
		root.Id:     {root, pending},
		old.Id:      {old},
	}

	var searchParams model.SearchParameter
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/posts/search", func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&searchParams))
		list := model.NewPostList()
		for _, post := range []*model.Post{old, answered, pending, own} {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/posts/", func(w http.ResponseWriter, r *http.Request) {
		rootID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v4/posts/"), "/thread")
		list := model.NewPostList()
		for _, post := range threads[rootID] {
			list.AddPost(post)
			list.AddOrder(post.Id)
		}
		writeTestJSON(w, list)
	})
	mux.HandleFunc("/api/v4/users/ids", usersByIDsHandler(t, []*model.User{me, alice}))
	mux.HandleFunc("/api/v4/channels/"+channel.Id, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, channel)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context(), UserID: me.Id, User: me}
	since := time.UnixMilli(base).UTC().Format(time.RFC3339)

	tests := []struct {
		name    string
		args    GetMentionsArgs
		want    []string
		notWant []string
	}{
		{
			name:    "since drops older mentions and the caller's own posts, newest first with reply status",
			args:    GetMentionsArgs{Since: since},
			want:    []string{"Found 2 mention(s)", "Mention 1** by alice (no reply from you in thread)", "deploy now", "Mention 2** by alice (you replied in thread)", "Channel: Ops"},
			notWant: []string{"ancient", "I said deploy"},
		},
		{
			name:    "unanswered_only keeps mentions without a later reply",
			args:    GetMentionsArgs{Since: since, UnansweredOnly: true},
			want:    []string{"Found 1 mention(s)", "deploy now"},
			notWant: []string{"@me look"},
		},
		{
			name: "limit caps the newest mentions",
			args: GetMentionsArgs{Since: since, Limit: 1},
			want: []string{"Found 2 mention(s), showing 1", "deploy now"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := provider.toolGetMentions(mcpCtx, tt.args)
			require.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, out, notWant)
			}
			require.NotNil(t, searchParams.Terms)
			assert.Contains(t, *searchParams.Terms, "@me")
			assert.Contains(t, *searchParams.Terms, "deploy")
			assert.Contains(t, *searchParams.Terms, "after:2026-03-09")
			require.NotNil(t, searchParams.PerPage)
			assert.Equal(t, mentionSearchPageSize, *searchParams.PerPage)
		})
	}
}
