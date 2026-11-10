// Package google signs people in with Google (OAuth 2.0 authorization code + PKCE, OpenID Connect userinfo).
package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	authorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL     = "https://oauth2.googleapis.com/token"
	userinfoURL  = "https://openidconnect.googleapis.com/v1/userinfo"
	maxBody      = 64 << 10
)

type Client struct {
	id, secret, redirect string
	http                 *http.Client
}

// New takes the OAuth client of Google Cloud; redirect is joseph-app's /api/auth/google/callback and must be listed
// among the client's authorized redirect URIs
func New(id, secret, redirect string) *Client {
	return &Client{id: id, secret: secret, redirect: redirect, http: &http.Client{Timeout: 10 * time.Second}}
}

var _ port.GoogleOAuth = (*Client)(nil)

func (c *Client) Configured() bool { return c.id != "" && c.secret != "" && c.redirect != "" }

func (c *Client) AuthorizationURL(state, codeChallenge string) string {
	query := url.Values{
		"client_id":             {c.id},
		"redirect_uri":          {c.redirect},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return authorizeURL + "?" + query.Encode()
}

func (c *Client) Exchange(ctx context.Context, code, codeVerifier string) (port.ExternalIdentity, error) {
	if !c.Configured() {
		return port.ExternalIdentity{}, domain.ErrGoogleNotConfigured
	}
	form := url.Values{
		"code":          {code},
		"code_verifier": {codeVerifier},
		"client_id":     {c.id},
		"client_secret": {c.secret},
		"redirect_uri":  {c.redirect},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return port.ExternalIdentity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.doJSON(req, &token); err != nil || token.AccessToken == "" {
		return port.ExternalIdentity{}, errors.Join(domain.ErrGoogleFailed, err)
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return port.ExternalIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	var user struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		HostedDomain  string `json:"hd"`
	}
	if err := c.doJSON(req, &user); err != nil {
		return port.ExternalIdentity{}, errors.Join(domain.ErrGoogleFailed, err)
	}
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if user.Subject == "" || email == "" || !user.EmailVerified {
		return port.ExternalIdentity{}, domain.ErrGoogleFailed
	}
	// Only mailboxes Google itself hosts (Gmail or Google Workspace): elsewhere Google cannot vouch for the address
	if !strings.HasSuffix(email, "@gmail.com") && user.HostedDomain == "" {
		return port.ExternalIdentity{}, domain.ErrGoogleFailed
	}
	return port.ExternalIdentity{Provider: domain.ProviderGoogle, Subject: user.Subject, Email: email, FullName: strings.TrimSpace(user.Name)}, nil
}

func (c *Client) doJSON(req *http.Request, out any) error {
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("google answered " + res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(out)
}
