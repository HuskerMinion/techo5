package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// script is one line of the script list: a name for the Show, and the command it runs here.
type script struct{ name, command string }

const scriptsHeader = `# TECHO5 Deck scripts: one per line, a name, an equals sign, and the command to run.
# Only what is listed here can be run from the deck, so keep this file to yourself.
# Edit and save; the Show sees the change next time it asks (or press Refresh on its setup page).
#
# Backup = robocopy "C:\Users\me\Documents" "D:\Backup\Documents" /MIR
# Lights = "C:\Tools\lights.exe" --scene evening
`

// ensureScriptsFile writes the list with its instructions the first time.
func ensureScriptsFile(path string) error {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(scriptsHeader), 0o600)
}

// readScripts reads the list. Lines starting with # are notes; a line without an equals sign, or
// with nothing on either side of it, is skipped. A name used twice keeps its first command.
func readScripts(path string) ([]script, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []script
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, cmd, ok := strings.Cut(line, "=")
		name, cmd = strings.TrimSpace(name), strings.TrimSpace(cmd)
		if !ok || name == "" || cmd == "" || len(name) > 100 {
			continue
		}
		if slices.ContainsFunc(out, func(s script) bool { return s.name == name }) {
			continue
		}
		out = append(out, script{name, cmd})
		if len(out) >= 500 {
			break
		}
	}
	return out, sc.Err()
}

func sortFold(s []string) {
	slices.SortFunc(s, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
}

// skipApp is an app not worth listing for a button: uninstallers, read-me files, help.
func skipApp(name string) bool {
	low := strings.ToLower(name)
	return strings.Contains(low, "uninstall") || strings.Contains(low, "readme") || strings.Contains(low, "help")
}

// start starts cmd and waits for it in the background, so a finished program doesn't stay behind as
// a zombie (or an open handle on Windows) for as long as the agent runs.
func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
