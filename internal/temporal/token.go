package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Entra accepts a JWT-SVID as a client assertion only with this audience.
const entraAssertionAudience = "api://AzureADTokenExchange"

// EntraToken trades this pod's SPIFFE JWT-SVID for an Entra access token, and caches it.
// Temporal reads the token's roles claim to decide what the controller may do.
type EntraToken struct {
	TenantID string
	ClientID string
	Scope    string

	// FetchSVID returns a signed JWT-SVID. SPIRESVID is the real one; tests swap in a fake.
	FetchSVID func(ctx context.Context) (string, error)
	// TokenURL defaults to Entra's v2 token endpoint for TenantID.
	TokenURL string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// SPIRESVID fetches a JWT-SVID from the SPIRE agent's Workload API socket.
func SPIRESVID(socket string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		svid, err := workloadapi.FetchJWTSVID(ctx,
			jwtsvid.Params{Audience: entraAssertionAudience},
			workloadapi.WithAddr("unix://"+socket))
		if err != nil {
			return "", fmt.Errorf("fetch JWT-SVID: %w", err)
		}
		return svid.Marshal(), nil
	}
}

// Token returns a cached token, or a fresh one when less than 5 minutes are left.
func (t *EntraToken) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Until(t.expires) > 5*time.Minute {
		return t.token, nil
	}

	assertion, err := t.FetchSVID(ctx)
	if err != nil {
		return "", err
	}
	tokenURL := t.TokenURL
	if tokenURL == "" {
		tokenURL = "https://login.microsoftonline.com/" + t.TenantID + "/oauth2/v2.0/token"
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {t.ClientID},
		"scope":                 {t.Scope},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("entra token request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("entra token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("entra token request returned %d: %s %s", resp.StatusCode, body.Error, body.Description)
	}

	t.token = body.AccessToken
	t.expires = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return t.token, nil
}

// GetHeaders lets the Temporal SDK attach the token to every call.
func (t *EntraToken) GetHeaders(ctx context.Context) (map[string]string, error) {
	token, err := t.Token(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}
