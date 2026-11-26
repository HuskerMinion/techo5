//go:build !dot && !spot

package diag

import "github.com/HuskerMinion/techo5/echod/internal/feature/cast"

// castNames is the names phones have given when casting here.
func castNames() []string { return cast.Get().SeenNames() }
