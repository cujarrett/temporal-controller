package temporal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fakeSVID = "svid-jwt"

func TestEntraTokenTradesSVIDAndCaches(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.PostForm.Get("client_assertion"); got != fakeSVID {
			t.Errorf("client_assertion = %q, want the JWT-SVID", got)
		}
		if got := r.PostForm.Get("scope"); got != "api://tenant/platform-temporal/.default" {
			t.Errorf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "entra-token", "expires_in": 3600})
	}))
	defer srv.Close()

	tok := &EntraToken{
		ClientID:  "client",
		Scope:     "api://tenant/platform-temporal/.default",
		FetchSVID: func(context.Context) (string, error) { return fakeSVID, nil },
		TokenURL:  srv.URL,
	}
	for range 2 {
		headers, err := tok.GetHeaders(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if headers["authorization"] != "Bearer entra-token" {
			t.Errorf("authorization = %q", headers["authorization"])
		}
	}
	if requests != 1 {
		t.Errorf("token requests = %d, want 1 because the second call uses the cache", requests)
	}
}

func TestEntraTokenReportsEntraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_client", "error_description": "no federated credential"})
	}))
	defer srv.Close()

	tok := &EntraToken{
		FetchSVID: func(context.Context) (string, error) { return fakeSVID, nil },
		TokenURL:  srv.URL,
	}
	if _, err := tok.Token(context.Background()); err == nil {
		t.Fatal("Token() returned no error for a 401 from Entra")
	}
}
