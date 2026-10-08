//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// listApps is the desktop's applications, from their .desktop files, by name. The ones the desktop
// hides (NoDisplay, Hidden) are left out.
func listApps() map[string]string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/usr/share/applications", "/usr/local/share/applications",
		"/var/lib/flatpak/exports/share/applications", "/var/lib/snapd/desktop/applications",
		filepath.Join(home, ".local/share/applications"),
		filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
	}
	apps := map[string]string{}
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
		for _, p := range matches {
			name, ok := desktopEntryName(p)
			if !ok || skipApp(name) {
				continue
			}
			if _, dup := apps[name]; !dup {
				apps[name] = p
			}
		}
	}
	return apps
}

// desktopEntryName is a .desktop file's Name, when it is an application the desktop shows.
func desktopEntryName(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	var name string
	in := false
	app := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == "[Desktop Entry]"
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			if name == "" {
				name = strings.TrimSpace(v)
			}
		case "Type":
			app = strings.TrimSpace(v) == "Application"
		case "NoDisplay", "Hidden":
			if strings.EqualFold(strings.TrimSpace(v), "true") {
				return "", false
			}
		}
	}
	return name, app && name != ""
}

// openApp starts an application from its .desktop file, as the desktop's own launcher would.
func openApp(path string) error {
	id := strings.TrimSuffix(filepath.Base(path), ".desktop")
	if gtk, err := exec.LookPath("gtk-launch"); err == nil {
		return start(exec.Command(gtk, id))
	}
	if gio, err := exec.LookPath("gio"); err == nil {
		return start(exec.Command(gio, "launch", path))
	}
	return errors.New("can't start apps here: install gtk-launch or gio")
}

func openURL(u string) error { return start(exec.Command("xdg-open", u)) }

func runScript(command string) error { return start(exec.Command("/bin/sh", "-c", command)) }

// setStartup puts the agent in the desktop's autostart folder, with args, or takes it out.
func setStartup(on bool, args []string) error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "autostart", "techo5-deck.desktop")
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
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	cmd := desktopQuote(exe)
	for _, a := range args {
		cmd += " " + desktopQuote(a)
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=TECHO5 Deck agent\nExec=%s\nTerminal=true\nX-GNOME-Autostart-enabled=true\n", cmd)
	return os.WriteFile(p, []byte(entry), 0o644)
}

// desktopQuote quotes one argument of a desktop entry's Exec line as the Desktop Entry spec has it:
// in double quotes with a backslash before " ` $ and \, and % doubled; then, since Exec is itself a
// string value of the file, each backslash doubled again.
func desktopQuote(a string) string {
	a = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`, `%`, `%%`).Replace(a)
	return strings.ReplaceAll(`"`+a+`"`, `\`, `\\`)
}
