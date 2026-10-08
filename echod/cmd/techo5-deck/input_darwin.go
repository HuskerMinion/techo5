//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// osascript runs a script, given on standard input so nothing of it (typed text least of all) is on
// a command line other programs can read, and says what macOS said when it refused: most often the
// missing Accessibility permission.
func osascript(lang, script string, secret bool) error {
	// A minute is more than any button's typing takes; an osascript that hangs mustn't hold the keys.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-l", lang, "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "-1743") || strings.Contains(msg, "-25211") || strings.Contains(msg, "-1719") || strings.Contains(msg, "not allowed") {
		return errors.New("macOS hasn't allowed it to press keys: System Settings > Privacy & Security > Accessibility, turn on the agent (or the Terminal it runs in)")
	}
	if secret || msg == "" {
		// What osascript says can quote the script, and with it the text.
		return fmt.Errorf("osascript failed (%v)", err)
	}
	return fmt.Errorf("osascript: %s", msg)
}

// canPressKeys asks System Events whether this program may control the computer.
func canPressKeys() bool {
	// Bounded: the first time, macOS holds the answer until somebody answers its permission prompt.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-")
	cmd.Stdin = strings.NewReader(`tell application "System Events" to get UI elements enabled`)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func pressCombo(c combo) error {
	lang, script, err := macKeyScript(c)
	if err != nil {
		return err
	}
	return osascript(lang, script, false)
}

// checkType says whether macOS lets the agent type at all (System Events types any text), so a
// missing permission reaches the Show rather than only this window.
func checkType(string) error {
	if !canPressKeys() {
		return errors.New("macOS hasn't allowed it to press keys: System Settings > Privacy & Security > Accessibility, turn on the agent (or the Terminal it runs in)")
	}
	return nil
}

func typeText(s string) error {
	script, err := macTypeScript(s)
	if err != nil {
		return err
	}
	return osascript("JavaScript", script, true)
}

// listApps is the apps in the Applications folders, by name.
func listApps() map[string]string {
	home, _ := os.UserHomeDir()
	apps := map[string]string{}
	for _, dir := range []string{"/Applications", "/Applications/Utilities", "/System/Applications",
		"/System/Applications/Utilities", filepath.Join(home, "Applications")} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
		for _, p := range matches {
			name := strings.TrimSuffix(filepath.Base(p), ".app")
			if _, dup := apps[name]; !dup && !skipApp(name) {
				apps[name] = p
			}
		}
	}
	return apps
}

func openApp(path string) error { return start(exec.Command("open", "-a", path)) }
func openURL(u string) error    { return start(exec.Command("open", u)) }

func runScript(command string) error { return start(exec.Command("/bin/sh", "-c", command)) }

const launchLabel = "org.techo5.deck"

// setStartup writes a LaunchAgent that starts the agent, with args, at sign-in, or removes it.
func setStartup(on bool, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	p := filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := "<string>" + xmlEscape(exe) + "</string>"
	for _, a := range args {
		argv += "<string>" + xmlEscape(a) + "</string>"
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
 <key>Label</key><string>%s</string>
 <key>ProgramArguments</key><array>%s</array>
 <key>RunAtLoad</key><true/>
 <key>KeepAlive</key><true/>
</dict></plist>
`, launchLabel, argv)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist), 0o644)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
