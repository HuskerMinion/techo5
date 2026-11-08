//go:build dot

// Package person is nothing on the Dot, which has no camera.
package person

import "github.com/HuskerMinion/techo5/echod/internal/config"

func State() (bool, int)               { return false, 0 }
func SetPerson(on bool)                { _ = config.Set().Presence().Person(on) }
func OnPerson(fn func(float64)) func() { return func() {} }
