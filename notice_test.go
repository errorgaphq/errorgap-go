package errorgap

import (
	"errors"
	"strings"
	"testing"
)

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

func TestBuildNoticeCapturesTypeAndMessage(t *testing.T) {
	cfg := Config{ProjectSlug: "demo", Environment: "test"}
	cfg.applyDefaults()
	n := buildNotice(&customError{msg: "boom"}, &cfg, NoticeOptions{})

	if got, want := n.Errors[0].Type, "customError"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if n.Errors[0].Message != "boom" {
		t.Errorf("Message = %q", n.Errors[0].Message)
	}
}

func TestBuildNoticeNotifierIdentification(t *testing.T) {
	cfg := Config{ProjectSlug: "demo", Environment: "test", Release: "1.2.3"}
	cfg.applyDefaults()
	n := buildNotice(errors.New("x"), &cfg, NoticeOptions{})

	if n.Context["notifier"] != "errorgap-go" {
		t.Errorf("notifier = %v", n.Context["notifier"])
	}
	if n.Context["notifier_version"] != Version {
		t.Errorf("notifier_version = %v", n.Context["notifier_version"])
	}
	if n.Context["environment"] != "test" {
		t.Errorf("environment = %v", n.Context["environment"])
	}
	if n.Context["release"] != "1.2.3" {
		t.Errorf("release = %v", n.Context["release"])
	}
}

func TestBuildNoticeFiltersSensitiveParams(t *testing.T) {
	cfg := Config{ProjectSlug: "demo"}
	cfg.applyDefaults()
	n := buildNotice(errors.New("x"), &cfg, NoticeOptions{
		Params: map[string]any{
			"username": "alice",
			"password": "hunter2",
			"nested":   map[string]any{"auth_token": "abc", "safe": "ok"},
		},
	})
	if n.Params["password"] != "[FILTERED]" {
		t.Error("password should be filtered")
	}
	nested := n.Params["nested"].(map[string]any)
	if nested["auth_token"] != "[FILTERED]" {
		t.Error("nested auth_token should be filtered")
	}
}

func TestBuildNoticeBacktrace(t *testing.T) {
	cfg := Config{ProjectSlug: "demo"}
	cfg.applyDefaults()
	n := buildNotice(errors.New("x"), &cfg, NoticeOptions{})
	if len(n.Errors[0].Backtrace) == 0 {
		t.Fatal("expected non-empty backtrace")
	}
	top := n.Errors[0].Backtrace[0]
	if !strings.Contains(top.Function, "buildNotice") &&
		!strings.Contains(top.Function, "TestBuildNoticeBacktrace") {
		t.Errorf("backtrace top function = %q", top.Function)
	}
}
