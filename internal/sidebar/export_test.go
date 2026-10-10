package sidebar

import "github.com/lvim-tech/mailforge/internal/config"

// pathForTest is the state file as the tests need to reach it: they tamper with the bytes on disk
// and compare modification times, which is the only way to prove "unreadable is not fatal" and
// "the same value is not written twice".
func pathForTest() string { return config.StatePath() }
