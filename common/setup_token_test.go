package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupTokenLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	t.Setenv(setupTokenFileEnv, path)
	t.Cleanup(ClearSetupToken)
	ClearSetupToken()

	if SetupTokenActive() {
		t.Fatalf("token should not be active before EnsureSetupToken")
	}

	token, err := EnsureSetupToken()
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	if token == "" {
		t.Fatalf("token should be non-empty")
	}
	if len(token) != 64 {
		t.Fatalf("token should be 64 hex chars, got %d", len(token))
	}
	if !SetupTokenActive() {
		t.Fatalf("token should be active")
	}

	// File must exist with mode 0600
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file should exist: %v", err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("token file mode = %v, want 0600", stat.Mode().Perm())
	}

	// Idempotent
	token2, err := EnsureSetupToken()
	if err != nil {
		t.Fatalf("EnsureSetupToken second call: %v", err)
	}
	if token2 != token {
		t.Fatalf("token should be stable across calls")
	}

	if VerifySetupToken("") {
		t.Fatalf("empty input must not verify")
	}
	if VerifySetupToken("wrong") {
		t.Fatalf("wrong input must not verify")
	}
	if !VerifySetupToken(token) {
		t.Fatalf("correct input must verify")
	}

	ClearSetupToken()
	if SetupTokenActive() {
		t.Fatalf("token should not be active after Clear")
	}
	if VerifySetupToken(token) {
		t.Fatalf("token must not verify after Clear")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should be removed after Clear, stat err=%v", err)
	}
}

func TestVerifySetupTokenWhenNotActive(t *testing.T) {
	t.Cleanup(ClearSetupToken)
	ClearSetupToken()

	if VerifySetupToken("anything") {
		t.Fatalf("verify must return false when no token is active")
	}
}