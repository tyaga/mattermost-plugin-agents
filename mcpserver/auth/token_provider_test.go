// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Flush() error         { return nil }

func TestTokenProviderValidatesOnce(t *testing.T) {
	userID := model.NewId()

	tests := []struct {
		name        string
		statuses    []int // response status for each GET /users/me, last one repeats
		calls       int
		wantGetMe   int32
		wantLastErr bool
	}{
		{"valid token is validated once", []int{http.StatusOK}, 3, 1, false},
		{"failed validation is retried", []int{http.StatusInternalServerError, http.StatusOK}, 3, 2, false},
		{"invalid token fails every time", []int{http.StatusUnauthorized}, 2, 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getMeCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v4/users/me" {
					http.NotFound(w, r)
					return
				}
				n := int(getMeCalls.Add(1)) - 1
				status := tt.statuses[min(n, len(tt.statuses)-1)]
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "user"})
			}))
			defer server.Close()

			provider := NewTokenAuthenticationProvider(server.URL, "", "token", noopLogger{})

			var lastClient *model.Client4
			var lastErr error
			for range tt.calls {
				client, user, err := provider.GetAuthenticatedClientAndUser(context.Background())
				lastErr = err
				if err != nil {
					continue
				}
				require.Equal(t, userID, user.Id)
				if lastClient != nil {
					require.Same(t, lastClient, client, "cached client must be reused")
				}
				lastClient = client
			}

			require.Equal(t, tt.wantGetMe, getMeCalls.Load())
			if tt.wantLastErr {
				require.Error(t, lastErr)
			} else {
				require.NoError(t, lastErr)
			}
		})
	}
}
