// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// apiRequestTimeout bounds one Mattermost API request, including file
// uploads; tool calls that page through history issue many requests, each
// under its own deadline.
const apiRequestTimeout = 2 * time.Minute

// sharedTransport is used by every Mattermost API client the auth providers
// build. One pool for the process keeps connections warm between tool calls
// and lets a single tool fan out several requests without a TLS handshake
// for each (the default transport keeps only two idle connections per host).
var sharedTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 32
	transport.ForceAttemptHTTP2 = true
	return transport
}()

// newAPIClient returns a Client4 for serverURL that uses the shared transport.
func newAPIClient(serverURL string) *model.Client4 {
	client := model.NewAPIv4Client(serverURL)
	client.HTTPClient = &http.Client{Transport: sharedTransport, Timeout: apiRequestTimeout}
	return client
}
