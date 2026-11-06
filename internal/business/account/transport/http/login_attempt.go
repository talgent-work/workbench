package http

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// loginAttempt is one sign-in in progress: the state GitHub must echo, the
// PKCE verifier and where to return. It travels in a signed short-lived cookie
// so any replica can complete it.
type loginAttempt struct {
	State     string `json:"s"`
	Verifier  string `json:"v"`
	Next      string `json:"n"`
	ExpiresAt int64  `json:"e"`
}

var errLoginAttemptInvalid = errors.New("login attempt invalid or expired")

type loginAttempts struct {
	key []byte
	ttl time.Duration
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (l loginAttempts) start(next string, now time.Time) (loginAttempt, error) {
	state, err := randomToken()
	if err != nil {
		return loginAttempt{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return loginAttempt{}, err
	}
	return loginAttempt{State: state, Verifier: verifier, Next: next, ExpiresAt: now.Add(l.ttl).Unix()}, nil
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (l loginAttempts) encode(attempt loginAttempt) (string, error) {
	payload, err := json.Marshal(attempt)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(l.mac(body)), nil
}

func (l loginAttempts) decode(value string, now time.Time) (loginAttempt, error) {
	body, signature, ok := strings.Cut(value, ".")
	if !ok {
		return loginAttempt{}, errLoginAttemptInvalid
	}
	mac, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(mac, l.mac(body)) {
		return loginAttempt{}, errLoginAttemptInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return loginAttempt{}, errLoginAttemptInvalid
	}
	var attempt loginAttempt
	if err := json.Unmarshal(payload, &attempt); err != nil || now.Unix() >= attempt.ExpiresAt {
		return loginAttempt{}, errLoginAttemptInvalid
	}
	return attempt, nil
}

func (l loginAttempts) mac(body string) []byte {
	h := hmac.New(sha256.New, l.key)
	h.Write([]byte(body))
	return h.Sum(nil)
}

const maxNextLength = 2048

// returnTargets are where a sign-in may return: the entries of the platform,
// each named by its origin, such as the Workbench entry that receives
// GitHub's callback and another product's entry beside it. A target belongs
// to the entry the sign-in started on, and never names the sign-in endpoints.
type returnTargets struct{ origins map[string]bool }

// newReturnTargets trusts the entry of the callback URL and the other entries' origins.
func newReturnTargets(callbackURL string, others []string) (returnTargets, error) {
	targets := returnTargets{origins: map[string]bool{}}
	if callback := strings.TrimSpace(callbackURL); callback != "" {
		parsed, err := url.Parse(callback)
		if err != nil || parsed.Host == "" {
			return returnTargets{}, fmt.Errorf("account auth endpoints: callback URL %q has no origin", callbackURL)
		}
		others = append([]string{parsed.Scheme + "://" + parsed.Host}, others...)
	}
	for _, raw := range others {
		origin := strings.TrimRight(strings.TrimSpace(raw), "/")
		parsed, err := url.Parse(origin)
		if origin == "" || err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return returnTargets{}, fmt.Errorf("account auth endpoints: return origin %q is not a scheme and host", raw)
		}
		targets.origins[parsed.Scheme+"://"+parsed.Host] = true
	}
	return targets, nil
}

// from is where a sign-in started on host returns: a trusted origin's page as
// asked, or a path on the entry served at host. Anything else returns to that
// entry's home, or to the callback host's when host is no entry.
func (t returnTargets) from(host, raw string) string {
	next := strings.TrimSpace(raw)
	if origin, path, ok := t.split(next); ok {
		return origin + internalPath(path)
	}
	for origin := range t.origins {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"), host) {
			return origin + internalPath(next)
		}
	}
	return internalPath(next)
}

// resolve keeps a recorded target on a trusted origin or this site, and out
// of the sign-in endpoints.
func (t returnTargets) resolve(raw string) string {
	next := strings.TrimSpace(raw)
	if origin, path, ok := t.split(next); ok {
		return origin + internalPath(path)
	}
	return internalPath(next)
}

// split separates a trusted origin from the path that follows it.
func (t returnTargets) split(next string) (origin, path string, ok bool) {
	parsed, err := url.Parse(next)
	if err != nil || parsed.Host == "" || parsed.User != nil || len(next) > maxNextLength || strings.ContainsAny(next, "\\\r\n\t") {
		return "", "", false
	}
	origin = parsed.Scheme + "://" + parsed.Host
	if !t.origins[origin] {
		return "", "", false
	}
	path = strings.TrimPrefix(next, origin)
	if path == "" {
		path = "/"
	}
	return origin, path, true
}

// signInPage is the sign-in page of the entry the target belongs to. A
// failure with no target, such as a lost attempt, stays on the callback host.
func (t returnTargets) signInPage(next string) (page, path string) {
	if origin, rest, ok := t.split(next); ok {
		return origin + "/login", rest
	}
	return "/login", next
}

// internalPath keeps a return target a path on its site and out of the
// sign-in endpoints; anything else returns to the site's home.
func internalPath(raw string) string {
	next := strings.TrimSpace(raw)
	if next == "" || len(next) > maxNextLength || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n\t") {
		return "/"
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return "/"
	}
	if parsed.Path == "/auth" || strings.HasPrefix(parsed.Path, "/auth/") || parsed.Path == "/login" {
		return "/"
	}
	return next
}
