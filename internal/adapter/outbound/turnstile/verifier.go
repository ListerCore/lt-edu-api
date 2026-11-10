// Package turnstile verifies Cloudflare Turnstile tokens on the server.
package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"api/internal/core/domain"
)

const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type Verifier struct {
	secret     string
	production bool
	client     *http.Client
	logger     zerolog.Logger
}

// New returns a verifier. Without a secret, development accepts every token and production rejects them all.
func New(secret string, production bool, logger zerolog.Logger) *Verifier {
	if secret == "" {
		if production {
			logger.Error().Msg("APP__TURNSTILE__SECRET_KEY is empty: every captcha check will fail")
		} else {
			logger.Warn().Msg("APP__TURNSTILE__SECRET_KEY is empty: captcha checks are skipped in development")
		}
	}
	return &Verifier{secret: secret, production: production, client: &http.Client{Timeout: 5 * time.Second}, logger: logger}
}

func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if v.secret == "" {
		if v.production {
			return domain.ErrCaptchaFailed
		}
		return nil
	}
	if token == "" {
		return domain.ErrCaptchaFailed
	}

	form := url.Values{"secret": {v.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteverifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := v.client.Do(req)
	if err != nil {
		// Fail closed: Cloudflare unreachable means no proof the caller is human
		v.logger.Warn().Err(err).Msg("turnstile siteverify unreachable")
		return domain.ErrCaptchaFailed
	}
	defer res.Body.Close()

	var body struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return domain.ErrCaptchaFailed
	}
	if !body.Success {
		v.logger.Info().Strs("error_codes", body.ErrorCodes).Msg("turnstile rejected token")
		return domain.ErrCaptchaFailed
	}
	return nil
}
