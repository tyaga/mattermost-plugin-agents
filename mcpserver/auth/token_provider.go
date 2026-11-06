// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
)

// TokenAuthenticationProvider provides PAT token authentication for STDIO transport.
//
// A personal access token names one user for the life of the process, so the
// client and the user it resolves to are validated once and reused by every
// tool call instead of costing a GET /users/me round trip each.
type TokenAuthenticationProvider struct {
	providerBase
	token string

	mu     sync.Mutex
	client *model.Client4
	user   *model.User
}

// NewTokenAuthenticationProvider creates a new PAT token authentication provider for STDIO transport
// Uses internalURL for API communication if provided, otherwise falls back to externalURL
func NewTokenAuthenticationProvider(externalURL, internalURL, token string, logger logger.Logger) *TokenAuthenticationProvider {
	return &TokenAuthenticationProvider{
		providerBase: newProviderBase(externalURL, internalURL, logger),
		token:        token,
	}
}

// ValidateAuth validates authentication (single GetMe call)
func (p *TokenAuthenticationProvider) ValidateAuth(ctx context.Context) error {
	return validateAuth(ctx, p)
}

// GetAuthenticatedMattermostClient returns an authenticated Mattermost client
func (p *TokenAuthenticationProvider) GetAuthenticatedMattermostClient(ctx context.Context) (*model.Client4, error) {
	client, _, err := p.GetAuthenticatedClientAndUser(ctx)
	return client, err
}

// GetAuthenticatedUser returns the user the token belongs to.
func (p *TokenAuthenticationProvider) GetAuthenticatedUser(ctx context.Context) (*model.User, error) {
	_, user, err := p.GetAuthenticatedClientAndUser(ctx)
	return user, err
}

// GetAuthenticatedClientAndUser returns the client and user for the token.
// The first successful validation is cached; a failed one is retried on the
// next call.
func (p *TokenAuthenticationProvider) GetAuthenticatedClientAndUser(ctx context.Context) (*model.Client4, *model.User, error) {
	if p.token == "" {
		return nil, nil, fmt.Errorf("no authentication token available")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, p.user, nil
	}

	client := newAPIClient(p.mmServerURL)
	client.SetToken(p.token)

	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		p.logger.Error("failed to validate token", "error", err)
		return nil, nil, fmt.Errorf("invalid authentication token: %w", err)
	}

	p.logger.Debug("validated token for user", "user_id", user.Id, "username", user.Username)

	p.client, p.user = client, user
	return client, user, nil
}
