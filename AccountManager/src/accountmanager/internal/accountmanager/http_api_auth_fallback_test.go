package accountmanager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type failingStdioAuthWriter struct{}

func (failingStdioAuthWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("stdio unavailable")
}

func TestAccountManagerIntrospectionFallsBackToHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fallback-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(accountManagerAuthIntrospection{
			Success:       true,
			Authenticated: true,
			Account:       "admin",
			Project:       "default",
		})
	}))
	defer server.Close()

	accountManagerStdioAuth.Lock()
	previousEnabled := accountManagerStdioAuth.enabled
	previousWriter := accountManagerStdioAuth.writer
	previousPending := accountManagerStdioAuth.pending
	accountManagerStdioAuth.enabled = true
	accountManagerStdioAuth.writer = failingStdioAuthWriter{}
	accountManagerStdioAuth.pending = map[string]chan accountManagerStdioAuthResponse{}
	accountManagerStdioAuth.Unlock()
	t.Cleanup(func() {
		accountManagerStdioAuth.Lock()
		accountManagerStdioAuth.enabled = previousEnabled
		accountManagerStdioAuth.writer = previousWriter
		accountManagerStdioAuth.pending = previousPending
		accountManagerStdioAuth.Unlock()
	})

	session, ok := accountManagerIntrospectTokenWithHost(server.URL, "fallback-token")
	if !ok || !session.Success || !session.Authenticated {
		t.Fatalf("expected HTTP fallback introspection to succeed: %+v ok=%t", session, ok)
	}
}

func TestResolveAccountManagerHostURLUsesTrustedProcessEnvironment(t *testing.T) {
	t.Setenv("AGENTIC_HOST_URL", "http://127.0.0.1:4080")
	t.Setenv("ACCOUNT_MANAGER_HOST_URL", "")
	t.Setenv("AGENTIC_BASE_URL", "")

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18186/api/account-manager/plugin/auth", nil)
	req.RemoteAddr = "127.0.0.1:53124"
	got := resolveAccountManagerHostURL(req, accountManagerHostAuthRequest{
		HostURL: "http://127.0.0.1:9999",
	})
	if got != "http://127.0.0.1:4080" {
		t.Fatalf("expected process environment host URL, got %q", got)
	}
}

func TestResolveAccountManagerHostURLDoesNotUseEphemeralRemotePort(t *testing.T) {
	t.Setenv("AGENTIC_HOST_URL", "")
	t.Setenv("ACCOUNT_MANAGER_HOST_URL", "")
	t.Setenv("AGENTIC_BASE_URL", "")

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18186/api/account-manager/plugin/auth", nil)
	req.RemoteAddr = "127.0.0.1:53124"
	got := resolveAccountManagerHostURL(req, accountManagerHostAuthRequest{})
	if got != "http://127.0.0.1" {
		t.Fatalf("expected host URL without ephemeral source port, got %q", got)
	}
}
