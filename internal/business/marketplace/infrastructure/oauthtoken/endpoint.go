// Package oauthtoken posts grants to an OAuth token endpoint (RFC 6749,
// section 3.2) for every Provider adapter: one client authentication, one
// response reading and one error shape.
package oauthtoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// timeout bounds one call to a token endpoint.
const timeout = 15 * time.Second

// ClientAuthentication is how the client proves itself to the token endpoint.
type ClientAuthentication int

const (
	// ClientSecretBasic sends the client's credentials with HTTP Basic; a
	// client without a secret is named in the form.
	ClientSecretBasic ClientAuthentication = iota
	// ClientSecretPost sends them in the form, as GitHub expects.
	ClientSecretPost
)

// Endpoint is one client's token endpoint.
type Endpoint struct {
	URL, ClientID, ClientSecret string
	Authentication              ClientAuthentication
}

// Token is a token endpoint's answer. A refresh credential's lifetime is
// refresh_expires_in or refresh_token_expires_in, as the server names it.
type Token struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	// RefreshTokenExpiresIn is GitHub's name for RefreshExpiresIn.
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	IDToken               string `json:"id_token"`
	Scope                 string `json:"scope"`
}

// RefreshLifetime is the refresh credential's lifetime in seconds, zero when
// the server reports none.
func (t Token) RefreshLifetime() int64 { return max(t.RefreshExpiresIn, t.RefreshTokenExpiresIn) }

// Error is an error the token endpoint answered: an OAuth error code, or the
// HTTP status of an answer without one.
type Error struct {
	StatusCode int
	Code       string
}

func (e *Error) Error() string { return fmt.Sprintf("oauth error %s (HTTP %d)", e.Code, e.StatusCode) }

// Refused reports an error answer whose code is one of codes.
func Refused(err error, codes ...string) bool {
	var answered *Error
	if !errors.As(err, &answered) {
		return false
	}
	for _, code := range codes {
		if answered.Code == code {
			return true
		}
	}
	return false
}

// Request posts a grant and reads the issued token. A server that answers an
// error in a successful response, as GitHub does, is answered as an Error.
func (e Endpoint) Request(ctx context.Context, client *http.Client, form url.Values) (Token, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := e.Post(ctx, e.URL, form)
	if err != nil {
		return Token{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Token{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Token{}, err
	}
	var answer struct {
		Token
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &answer) != nil && response.StatusCode < 300 {
		return Token{}, fmt.Errorf("oauth token response is not JSON (HTTP %d)", response.StatusCode)
	}
	if answer.Error != "" || response.StatusCode >= 300 {
		return Token{}, &Error{StatusCode: response.StatusCode, Code: answer.Error}
	}
	if strings.TrimSpace(answer.AccessToken) == "" {
		return Token{}, &Error{StatusCode: response.StatusCode, Code: "incomplete_response"}
	}
	return answer.Token, nil
}

// Post is a form request to one of the client's endpoints, authenticated as
// the client; revocation (RFC 7009) authenticates the same way.
func (e Endpoint) Post(ctx context.Context, address string, form url.Values) (*http.Request, error) {
	switch {
	case e.Authentication == ClientSecretPost:
		form.Set("client_id", e.ClientID)
		form.Set("client_secret", e.ClientSecret)
	case e.ClientSecret == "":
		form.Set("client_id", e.ClientID)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	if e.Authentication == ClientSecretBasic && e.ClientSecret != "" {
		request.SetBasicAuth(url.QueryEscape(e.ClientID), url.QueryEscape(e.ClientSecret))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return request, nil
}
