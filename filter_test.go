package errorgap

import "testing"

func TestFilterMasksKeys(t *testing.T) {
	defaults := []string{"password", "token", "secret", "api_key", "authorization", "cookie"}
	out := FilterParams(map[string]any{
		"username":     "alice",
		"password":     "hunter2",
		"access_token": "x",
	}, defaults)
	if out["username"] != "alice" {
		t.Error("username should be preserved")
	}
	if out["password"] != "[FILTERED]" {
		t.Errorf("password = %v, want [FILTERED]", out["password"])
	}
	if out["access_token"] != "[FILTERED]" {
		t.Errorf("access_token = %v, want [FILTERED]", out["access_token"])
	}
}

func TestFilterRecursesNested(t *testing.T) {
	defaults := []string{"api_key"}
	out := FilterParams(map[string]any{
		"user": map[string]any{
			"name":    "alice",
			"api_key": "x",
		},
	}, defaults)
	nested := out["user"].(map[string]any)
	if nested["api_key"] != "[FILTERED]" {
		t.Error("nested api_key should be filtered")
	}
	if nested["name"] != "alice" {
		t.Error("nested name should be preserved")
	}
}

func TestFilterCaseInsensitive(t *testing.T) {
	defaults := []string{"authorization"}
	out := FilterParams(map[string]any{"Authorization": "Bearer xyz"}, defaults)
	if out["Authorization"] != "[FILTERED]" {
		t.Error("Authorization (uppercase) should be filtered")
	}
}
