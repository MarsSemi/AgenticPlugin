package accountmanager

import "testing"

func TestAccountManagerJWTRequestUsesConfiguredClaimsAndExtensions(t *testing.T) {
	account := managedAccount{
		ID:          "account-1",
		Username:    "operator",
		DisplayName: "Operator",
		Metadata: map[string]any{
			"jwt": map[string]any{
				"enabled": true,
				"claims": map[string]any{
					"department": "factory",
				},
				"extensions": []any{
					map[string]any{"tenant": "fab-1"},
					map[string]any{"features": []any{"audit"}},
				},
			},
		},
	}
	payload, extensions, requested, err := accountManagerJWTRequest(
		account,
		"default",
		[]string{"user"},
		[]pluginPermission{{PluginID: "terminal", Enabled: true, Scopes: []string{"read"}}},
		[]string{"default"},
	)
	if err != nil {
		t.Fatalf("accountManagerJWTRequest failed: %v", err)
	}
	if !requested || payload["iss"] != "operator" || payload["department"] != "factory" {
		t.Fatalf("unexpected JWT payload: %#v", payload)
	}
	if len(extensions) != 2 || extensions[0]["tenant"] != "fab-1" {
		t.Fatalf("unexpected JWT extensions: %#v", extensions)
	}
}

func TestAccountManagerJWTRequestIsOptIn(t *testing.T) {
	_, _, requested, err := accountManagerJWTRequest(
		managedAccount{Username: "operator"},
		"default",
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("accountManagerJWTRequest failed: %v", err)
	}
	if requested {
		t.Fatal("JWT request must remain disabled without metadata.jwt.enabled")
	}
}

func TestAccountManagerJWTRequestRejectsMoreThanTwoExtensions(t *testing.T) {
	account := managedAccount{
		Username: "operator",
		Metadata: map[string]any{
			"jwt": map[string]any{
				"enabled":    true,
				"extensions": []any{map[string]any{}, map[string]any{}, map[string]any{}},
			},
		},
	}
	if _, _, _, err := accountManagerJWTRequest(account, "default", nil, nil, nil); err == nil {
		t.Fatal("expected more than two extensions to be rejected")
	}
}
