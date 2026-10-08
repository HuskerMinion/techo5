//go:build !windows && !linux && !darwin

package main

import (
	"errors"
	"os/exec"
	"runtime"
)

// Anything but Windows, Linux and macOS runs the agent for websites and scripts, and presses no keys.

var errNotHere = errors.New("pressing keys isn't on " + runtime.GOOS)

func canPressKeys() bool              { return false }
func checkType(string) error          { return errNotHere }
func pressCombo(combo) error          { return errNotHere }
func typeText(string) error           { return errNotHere }
func listApps() map[string]string     { return map[string]string{} }
func openApp(string) error            { return errNotHere }
func setStartup(bool, []string) error { return errNotHere }
func openURL(u string) error          { return start(exec.Command("xdg-open", u)) }

func runScript(command string) error { return start(exec.Command("/bin/sh", "-c", command)) }
