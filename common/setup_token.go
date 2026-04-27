package common

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// setupTokenMu protects setupTokenValue and setupTokenFile.
var (
	setupTokenMu    sync.RWMutex
	setupTokenValue string
	setupTokenFile  string
)

const setupTokenFileDefault = "setup_token"
const setupTokenFileEnv = "SETUP_TOKEN_FILE"

// SetupTokenFilePath returns the location of the on-disk setup token file.
// Override with the SETUP_TOKEN_FILE environment variable.
func SetupTokenFilePath() string {
	if v := strings.TrimSpace(os.Getenv(setupTokenFileEnv)); v != "" {
		return v
	}
	return setupTokenFileDefault
}

// EnsureSetupToken generates a fresh 32-byte hex setup token, stores it in
// memory, writes it to a 0600 file (best-effort), and returns the token.
// Callers should print the token to the operator log so the operator can use
// it as the X-Setup-Token header on POST /api/setup.
//
// If a token is already in memory, EnsureSetupToken returns the existing one
// (idempotent across restarts of the same process); persistent storage is
// reset only on ClearSetupToken or process exit.
func EnsureSetupToken() (string, error) {
	setupTokenMu.Lock()
	defer setupTokenMu.Unlock()

	if setupTokenValue != "" {
		return setupTokenValue, nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	path := SetupTokenFilePath()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0700)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		// File write failed (read-only FS, perms, etc). Token is still kept in memory;
		// operator will see it from the log line. Surface the error so caller can warn.
		setupTokenValue = token
		setupTokenFile = ""
		return token, err
	}
	setupTokenValue = token
	setupTokenFile = path
	return token, nil
}

// VerifySetupToken returns true iff the input matches the active setup token
// using constant-time comparison. Returns false if no setup token is active.
func VerifySetupToken(input string) bool {
	setupTokenMu.RLock()
	defer setupTokenMu.RUnlock()
	if setupTokenValue == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(input), []byte(setupTokenValue)) == 1
}

// ClearSetupToken zeros the in-memory token and removes the on-disk file.
// Called once initial setup completes successfully so a subsequent /api/setup
// cannot be issued (even if the in-memory state were somehow re-armed).
func ClearSetupToken() {
	setupTokenMu.Lock()
	defer setupTokenMu.Unlock()
	setupTokenValue = ""
	if setupTokenFile != "" {
		_ = os.Remove(setupTokenFile)
		setupTokenFile = ""
	}
}

// SetupTokenActive reports whether a setup token is currently armed.
// Used to decide if the /api/setup endpoint should require the header.
func SetupTokenActive() bool {
	setupTokenMu.RLock()
	defer setupTokenMu.RUnlock()
	return setupTokenValue != ""
}

// ErrSetupTokenInvalid is returned by handlers when the X-Setup-Token header
// is missing or does not match.
var ErrSetupTokenInvalid = errors.New("invalid or missing setup token")