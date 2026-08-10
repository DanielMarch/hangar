package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OAuthConfig is HANGAR's confidential-client EVE SSO configuration
// (01_ARCHITECTURE.md §7.1): HTTP Basic client authentication *and* PKCE,
// not one or the other.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	AuthorizeURL string
	TokenURL     string
	HTTPClient   *http.Client
}

func (c OAuthConfig) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// AuthorizeURL builds the EVE SSO authorization redirect URL for state and
// codeChallenge (S256). scopeList is space-joined verbatim — scopes are
// opaque strings (internal/scopes), never inspected here.
func (c OAuthConfig) AuthorizeURLFor(state, codeChallenge string, scopeList []string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", c.ClientID)
	v.Set("redirect_uri", c.CallbackURL)
	v.Set("scope", strings.Join(scopeList, " "))
	v.Set("state", state)
	v.Set("code_challenge", codeChallenge)
	v.Set("code_challenge_method", "S256")
	return c.AuthorizeURL + "?" + v.Encode()
}

// TokenResponse is EVE SSO's token endpoint response shape, common to both
// the authorization_code and refresh_token grants.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// oauthError is the RFC 6749 error response shape, notably including
// "invalid_grant" — §7.3's do-not-retry signal.
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// GrantError wraps an OAuth error response so callers can distinguish
// invalid_grant (mark invalid, fire revocation, never retry — §7.3) from a
// transient failure.
type GrantError struct {
	Code        string
	Description string
}

func (e *GrantError) Error() string {
	return fmt.Sprintf("sso: oauth error %q: %s", e.Code, e.Description)
}

// IsInvalidGrant reports whether err is a GrantError with code
// "invalid_grant".
func IsInvalidGrant(err error) bool {
	var ge *GrantError
	if ok := asGrantError(err, &ge); ok {
		return ge.Code == "invalid_grant"
	}
	return false
}

func asGrantError(err error, target **GrantError) bool {
	for err != nil {
		if ge, ok := err.(*GrantError); ok {
			*target = ge
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ExchangeCode performs the Authorization Code + PKCE token exchange
// (§7.1): HTTP Basic client auth AND the code_verifier, both present.
func (c OAuthConfig) ExchangeCode(ctx context.Context, code, codeVerifier string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	return c.doTokenRequest(ctx, form)
}

// RefreshToken performs the refresh_token grant. EVE SSO rotates the
// refresh token on every use (§7.3) — the returned TokenResponse.RefreshToken
// is a NEW token; the one passed in is now dead regardless of outcome.
func (c OAuthConfig) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.doTokenRequest(ctx, form)
}

func (c OAuthConfig) doTokenRequest(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("sso: building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret) // confidential client (§7.1)

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("sso: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sso: reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oe oauthError
		if err := json.Unmarshal(body, &oe); err == nil && oe.Error != "" {
			return nil, &GrantError{Code: oe.Error, Description: oe.ErrorDescription}
		}
		return nil, fmt.Errorf("sso: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("sso: unmarshalling token response: %w", err)
	}
	return &tr, nil
}
