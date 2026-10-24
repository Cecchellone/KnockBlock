package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenURL is Discord's OAuth2 token endpoint.
const tokenURL = "https://discord.com/api/oauth2/token"

// exchange posts form (plus the client credentials) to the token endpoint.
func exchange(ctx context.Context, clientID, secret string, form map[string]string) (Token, error) {
	v := url.Values{"client_id": {clientID}, "client_secret": {secret}}
	for k, val := range form {
		v.Set(k, val)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(v.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("discord: token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("discord: token exchange: %s: %s", resp.Status, body)
	}
	var t struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.Access == "" {
		return Token{}, fmt.Errorf("discord: token exchange: unexpected answer")
	}
	return Token{Access: t.Access, Refresh: t.Refresh, Expiry: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}, nil
}
