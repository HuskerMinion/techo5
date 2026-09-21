//go:build dot

package diag

import "github.com/HuskerMinion/techo5/echod/internal/config"

// screenSettings: the Dot has no panel, so it has none of these.
func screenSettings(config.Config) []string { return nil }
