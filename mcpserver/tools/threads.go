// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetThreadsArgs represents arguments for the get_threads tool.
type GetThreadsArgs struct {
	TeamID     string `json:"team_id" jsonschema:"The team whose threads inbox to read,minLength=26,maxLength=26"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"Only return unread threads (default: false)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Number of threads to return (default: 30, max: 100),minimum=1,maximum=100"`
}

// GetMentionsArgs represents arguments for the get_mentions tool.
type GetMentionsArgs struct {
	TeamID         string `json:"team_id,omitempty" jsonschema:"Optional team ID to limit the search scope,maxLength=26"`
	Since          string `json:"since,omitempty" jsonschema:"Only mentions posted at or after this time (ISO 8601 / RFC3339),format=date-time"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of mentions to return, newest first (default: 20, max: 100),minimum=1,maximum=100"`
	UnansweredOnly bool   `json:"unanswered_only,omitempty" jsonschema:"Keep only mentions you have not replied to in their thread"`
}

// GetUnreadCountsArgs represents arguments for the get_unread_counts tool.
type GetUnreadCountsArgs struct {
	TeamID string `json:"team_id,omitempty" jsonschema:"Optional team ID; omit for unread counts across all teams,maxLength=26"`
}

// GetChannelUnreadArgs represents arguments for the get_channel_unread tool.
type GetChannelUnreadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to read unread counts for,minLength=26,maxLength=26"`
}

// GetPostsAroundUnreadArgs represents arguments for the get_posts_around_unread tool.
type GetPostsAroundUnreadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to read around the last-read line,minLength=26,maxLength=26"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Posts to fetch before and after the unread line (default: 20, max: 100),minimum=1,maximum=100"`
}

// MarkChannelReadArgs represents arguments for the mark_channel_read tool.
type MarkChannelReadArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel to mark viewed/read,minLength=26,maxLength=26"`
}

// MarkChannelsViewedArgs represents arguments for the mark_channels_viewed tool.
type MarkChannelsViewedArgs struct {
	ChannelIDs []string `json:"channel_ids" jsonschema:"The channels to mark viewed (bulk)"`
}

// MarkPostUnreadArgs represents arguments for the mark_post_unread tool.
type MarkPostUnreadArgs struct {
	PostID string `json:"post_id" jsonschema:"Mark the channel unread starting at this post,minLength=26,maxLength=26"`
}

// SetThreadFollowArgs represents arguments for the set_thread_follow tool.
type SetThreadFollowArgs struct {
	TeamID   string `json:"team_id" jsonschema:"The team the thread belongs to,minLength=26,maxLength=26"`
	ThreadID string `json:"thread_id" jsonschema:"The root post ID of the thread,minLength=26,maxLength=26"`
	Follow   bool   `json:"follow" jsonschema:"true to follow the thread, false to unfollow"`
}

const (
	getThreadsDescription           = "Get your collated threads inbox (followed and unread threads, CRT / collapsed reply threads) for a team. Parameters: team_id (required), unread_only (optional), limit. Returns each thread's root post, reply count, and unread counts."
	getMentionsDescription          = "Get posts that mention you (your @username or notification keywords), newest first, with channel name and whether you have replied in the post's thread since. Parameters: team_id (optional), since (optional ISO 8601), limit (1-100, default 20), unanswered_only (optional). Example: {\"since\": \"2024-01-01T00:00:00Z\", \"unanswered_only\": true}"
	getUnreadCountsDescription      = "Get your unread message, mention, and thread counts across teams, or for one team. Parameters: team_id (optional)."
	getChannelUnreadDescription     = "Get your unread message and mention counts for one channel. Parameters: channel_id (required)."
	getPostsAroundUnreadDescription = "Get the posts surrounding your last-read line in a channel. Parameters: channel_id (required), limit. Returns posts before and after the unread marker."
	markChannelReadDescription      = "Mark a channel viewed/read to clear its unread badge. Parameters: channel_id (required)."
	markChannelsViewedDescription   = "Mark one or more channels viewed/read (bulk). Parameters: channel_ids (required list)."
	markPostUnreadDescription       = "Mark a channel unread starting at a specific post. Parameters: post_id (required)."
	setThreadFollowDescription      = "Follow or unfollow a thread. Parameters: team_id (required), thread_id (root post ID, required), follow (true/false)."
)

// getThreadTools returns the threads, mentions, and unread-state tools.
func (p *MattermostToolProvider) getThreadTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, "get_threads", getThreadsDescription, p.toolGetThreads),
		mcpReadTool(p, "get_mentions", getMentionsDescription, p.toolGetMentions),
		mcpReadTool(p, "get_unread_counts", getUnreadCountsDescription, p.toolGetUnreadCounts),
		mcpReadTool(p, "get_channel_unread", getChannelUnreadDescription, p.toolGetChannelUnread),
		mcpReadTool(p, "get_posts_around_unread", getPostsAroundUnreadDescription, p.toolGetPostsAroundUnread),
		mcpTool(p, "mark_channel_read", markChannelReadDescription, p.toolMarkChannelRead),
		mcpTool(p, "mark_channels_viewed", markChannelsViewedDescription, p.toolMarkChannelsViewed),
		mcpTool(p, "mark_post_unread", markPostUnreadDescription, p.toolMarkPostUnread),
		mcpTool(p, "set_thread_follow", setThreadFollowDescription, p.toolSetThreadFollow),
	}
}

// toolGetThreads implements the get_threads tool.
func (p *MattermostToolProvider) toolGetThreads(mcpContext *MCPToolContext, args GetThreadsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	pageSize := uint64(30)
	if args.Limit > 0 {
		pageSize = uint64(args.Limit)
	}
	if pageSize > 100 {
		pageSize = 100
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	opts := model.GetUserThreadsOpts{
		PageSize: pageSize,
		Extended: true,
		Unread:   args.UnreadOnly,
	}
	threads, _, err := mcpContext.Client.GetUserThreads(mcpContext.Ctx, userID, args.TeamID, opts)
	if err != nil {
		return "", fmt.Errorf("error fetching threads: %w", err)
	}

	if threads == nil || len(threads.Threads) == 0 {
		return "no threads found", nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Threads inbox: %d total, %d unread threads, %d unread mentions\n\n",
		threads.Total, threads.TotalUnreadThreads, threads.TotalUnreadMentions)
	authorIDs := make([]string, 0, len(threads.Threads))
	for _, tr := range threads.Threads {
		if tr.Post != nil {
			authorIDs = append(authorIDs, tr.Post.UserId)
		}
	}
	usernames := p.usernamesByIDs(mcpContext.Ctx, mcpContext.Client, authorIDs)
	for i, tr := range threads.Threads {
		username := ""
		if tr.Post != nil {
			username = usernames[tr.Post.UserId]
		}
		format.WriteThreadSummary(&result, format.ThreadSummaryEntry{
			HeaderLabel: fmt.Sprintf("Thread %d", i+1),
			Thread:      tr,
			Username:    username,
		})
	}
	return result.String(), nil
}

// toolGetMentions implements the get_mentions tool. There is no dedicated mentions
// endpoint, so this searches for posts containing the user's mention keywords.
func (p *MattermostToolProvider) toolGetMentions(mcpContext *MCPToolContext, args GetMentionsArgs) (string, error) {
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}

	user, err := p.resolveUser(mcpContext)
	if err != nil {
		return "", err
	}

	keywords := mentionKeywords(user)
	if len(keywords) == 0 {
		return "no mention keywords configured for your account", nil
	}

	sinceMillis := int64(0)
	if args.Since != "" {
		if sinceMillis, err = parseTimeMillis(args.Since); err != nil {
			return "", err
		}
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// OR-search across all mention keywords. The after: modifier narrows
	// the search to whole days; the exact cut-off is applied below.
	terms := strings.Join(keywords, " ")
	if sinceMillis > 0 {
		terms += " after:" + time.UnixMilli(sinceMillis).UTC().AddDate(0, 0, -1).Format("2006-01-02")
	}
	page, perPage, isOr := 0, mentionSearchPageSize, true
	postList, _, err := mcpContext.Client.SearchPostsWithParams(mcpContext.Ctx, args.TeamID, &model.SearchParameter{
		Terms: &terms, IsOrSearch: &isOr, Page: &page, PerPage: &perPage,
	})
	if err != nil {
		return "", fmt.Errorf("error searching mentions: %w", err)
	}

	posts := make([]*model.Post, 0, len(postList.Posts))
	for _, post := range postList.Posts {
		if post.CreateAt >= sinceMillis && post.UserId != user.Id {
			posts = append(posts, post)
		}
	}
	slices.SortFunc(posts, func(a, b *model.Post) int {
		return cmp.Or(cmp.Compare(b.CreateAt, a.CreateAt), cmp.Compare(b.Id, a.Id))
	})

	replied := p.repliedInThread(mcpContext, user.Id, posts)
	if args.UnansweredOnly {
		posts = slices.DeleteFunc(posts, func(post *model.Post) bool { return replied[post.Id] })
	}
	total := len(posts)
	if total > args.Limit {
		posts = posts[:args.Limit]
	}
	if total == 0 {
		if args.UnansweredOnly {
			return "no unanswered mentions found", nil
		}
		return "no mentions found", nil
	}

	return p.formatMentions(mcpContext, posts, replied, total), nil
}

// mentionSearchPageSize is how many search hits get_mentions inspects. The
// search endpoint returns its default page (60) when no size is given.
const mentionSearchPageSize = 200

// repliedInThread reports, per post, whether userID posted in the post's
// thread after it. Threads are read a few at a time; a thread that cannot be
// read counts as not replied.
func (p *MattermostToolProvider) repliedInThread(mcpContext *MCPToolContext, userID string, posts []*model.Post) map[string]bool {
	roots := make([]string, 0, len(posts))
	seen := make(map[string]bool, len(posts))
	for _, post := range posts {
		root := cmp.Or(post.RootId, post.Id)
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}

	// Latest time the user posted in each thread.
	lastByUser := make([]int64, len(roots))
	forEachParallel(len(roots), func(i int) {
		thread, _, err := mcpContext.Client.GetPostThread(mcpContext.Ctx, roots[i], "", false)
		if err != nil {
			p.logger.Warn("failed to read thread for reply check", "root_id", roots[i], "error", err)
			return
		}
		for _, post := range thread.Posts {
			if post.UserId == userID && post.CreateAt > lastByUser[i] {
				lastByUser[i] = post.CreateAt
			}
		}
	})
	lastReply := make(map[string]int64, len(roots))
	for i, root := range roots {
		lastReply[root] = lastByUser[i]
	}

	replied := make(map[string]bool, len(posts))
	for _, post := range posts {
		replied[post.Id] = lastReply[cmp.Or(post.RootId, post.Id)] > post.CreateAt
	}
	return replied
}

// formatMentions renders mentions newest first with author, channel, and the
// caller's reply status.
func (p *MattermostToolProvider) formatMentions(mcpContext *MCPToolContext, posts []*model.Post, replied map[string]bool, total int) string {
	ctx, client := mcpContext.Ctx, mcpContext.Client

	userIDs := make([]string, len(posts))
	for i, post := range posts {
		userIDs[i] = post.UserId
	}
	channelNames := make([]string, len(posts))
	var usernames map[string]string
	var wg sync.WaitGroup
	wg.Go(func() { usernames = p.usernamesByIDs(ctx, client, userIDs) })
	wg.Go(func() {
		forEachParallel(len(posts), func(i int) {
			if channel, err := p.channel(ctx, client, mcpContext.UserID, posts[i].ChannelId); err == nil {
				channelNames[i] = channelDisplayNameOrFallback(channel)
			}
		})
	})
	wg.Wait()

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d mention(s), showing %d, newest first:\n\n", total, len(posts))
	for i, post := range posts {
		status := "(no reply from you in thread)"
		if replied[post.Id] {
			status = "(you replied in thread)"
		}
		format.WritePost(&result, format.PostEntry{
			Compact:         p.compactOutput,
			HeaderLabel:     fmt.Sprintf("Mention %d", i+1),
			Username:        usernames[post.UserId],
			ReplyAnnotation: status,
			Post:            post,
			ChannelName:     channelNames[i],
			ShowChannel:     true,
		})
	}
	return result.String()
}

// mentionKeywords builds the set of terms that mention a user: their @username plus
// any configured notification keywords.
func mentionKeywords(user *model.User) []string {
	seen := make(map[string]bool)
	var keywords []string
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		keywords = append(keywords, term)
	}

	add("@" + user.Username)
	if user.NotifyProps != nil {
		for kw := range strings.SplitSeq(user.NotifyProps["mention_keys"], ",") {
			add(kw)
		}
	}
	return keywords
}

// toolGetUnreadCounts implements the get_unread_counts tool.
func (p *MattermostToolProvider) toolGetUnreadCounts(mcpContext *MCPToolContext, args GetUnreadCountsArgs) (string, error) {
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var result strings.Builder
	if args.TeamID != "" {
		unread, _, uErr := client.GetTeamUnread(ctx, args.TeamID, userID)
		if uErr != nil {
			return "", fmt.Errorf("error fetching team unread counts: %w", uErr)
		}
		format.WriteTeamUnread(&result, unread)
		return result.String(), nil
	}

	unreads, _, uErr := client.GetTeamsUnreadForUser(ctx, userID, "", true)
	if uErr != nil {
		return "", fmt.Errorf("error fetching unread counts: %w", uErr)
	}
	if len(unreads) == 0 {
		return "no unread messages across your teams", nil
	}
	fmt.Fprintf(&result, "Unread counts across %d team(s):\n", len(unreads))
	for _, unread := range unreads {
		format.WriteTeamUnread(&result, unread)
	}
	return result.String(), nil
}

// toolGetChannelUnread implements the get_channel_unread tool.
func (p *MattermostToolProvider) toolGetChannelUnread(mcpContext *MCPToolContext, args GetChannelUnreadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	unread, _, err := mcpContext.Client.GetChannelUnread(mcpContext.Ctx, args.ChannelID, userID)
	if err != nil {
		return "", fmt.Errorf("error fetching channel unread counts: %w", err)
	}

	var result strings.Builder
	format.WriteChannelUnread(&result, unread)
	return result.String(), nil
}

// toolGetPostsAroundUnread implements the get_posts_around_unread tool.
func (p *MattermostToolProvider) toolGetPostsAroundUnread(mcpContext *MCPToolContext, args GetPostsAroundUnreadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	postList, _, err := mcpContext.Client.GetPostsAroundLastUnread(mcpContext.Ctx, userID, args.ChannelID, args.Limit, args.Limit, false)
	if err != nil {
		return "", fmt.Errorf("error fetching posts around unread: %w", err)
	}

	return p.formatPostListChrono(mcpContext, postList, "posts around your last-read line"), nil
}

// toolMarkChannelRead implements the mark_channel_read tool.
func (p *MattermostToolProvider) toolMarkChannelRead(mcpContext *MCPToolContext, args MarkChannelReadArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, _, err := mcpContext.Client.ViewChannel(mcpContext.Ctx, userID, &model.ChannelView{ChannelId: args.ChannelID}); err != nil {
		return "", fmt.Errorf("error marking channel read: %w", err)
	}

	return fmt.Sprintf("Successfully marked channel %s as read", args.ChannelID), nil
}

// toolMarkChannelsViewed implements the mark_channels_viewed tool.
func (p *MattermostToolProvider) toolMarkChannelsViewed(mcpContext *MCPToolContext, args MarkChannelsViewedArgs) (string, error) {
	if err := requireIDs("channel_ids", args.ChannelIDs); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	for _, id := range args.ChannelIDs {
		if _, _, err := mcpContext.Client.ViewChannel(mcpContext.Ctx, userID, &model.ChannelView{ChannelId: id}); err != nil {
			return "", fmt.Errorf("error marking channel %s viewed: %w", id, err)
		}
	}

	return fmt.Sprintf("Successfully marked %d channel(s) viewed", len(args.ChannelIDs)), nil
}

// toolMarkPostUnread implements the mark_post_unread tool.
func (p *MattermostToolProvider) toolMarkPostUnread(mcpContext *MCPToolContext, args MarkPostUnreadArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.SetPostUnread(mcpContext.Ctx, userID, args.PostID, true); err != nil {
		return "", fmt.Errorf("error marking post unread: %w", err)
	}

	return fmt.Sprintf("Successfully marked channel unread starting at post %s", args.PostID), nil
}

// toolSetThreadFollow implements the set_thread_follow tool.
func (p *MattermostToolProvider) toolSetThreadFollow(mcpContext *MCPToolContext, args SetThreadFollowArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireID("thread_id", args.ThreadID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.UpdateThreadFollowForUser(mcpContext.Ctx, userID, args.TeamID, args.ThreadID, args.Follow); err != nil {
		return "", fmt.Errorf("error updating thread follow state: %w", err)
	}

	verb := "unfollowed"
	if args.Follow {
		verb = "followed"
	}
	return fmt.Sprintf("Successfully %s thread %s", verb, args.ThreadID), nil
}
