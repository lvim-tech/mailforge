package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points XDG_CONFIG_HOME at a scratch directory for the WHOLE package.
//
// The same reason as internal/account's: the registry's location comes from config.Path(), not from
// the Config a view holds, so any test that lets a view save would write over the real accounts.json
// of whoever ran the tests. That has happened once already, and it cost four configured accounts.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mailforge-ui-test-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config")); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
