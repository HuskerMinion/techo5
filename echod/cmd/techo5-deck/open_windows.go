//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// listApps is the Start menu's apps by name, as Windows lists them (Get-StartApps): the installed
// programs and the Store apps alike, each opened through the Start menu's own folder. Where that
// can't be asked, the Start menu's shortcut files.
func listApps() map[string]string {
	if apps := startApps(); len(apps) > 0 {
		return apps
	}
	return shortcutApps()
}

func startApps() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// UTF-8 out, or Windows PowerShell writes the console's code page and names with accents arrive
	// garbled.
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8; Get-StartApps | Select-Object Name, AppID | ConvertTo-Json -Compress")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var list []struct{ Name, AppID string }
	if json.Unmarshal(out, &list) != nil {
		var one struct{ Name, AppID string }
		if json.Unmarshal(out, &one) != nil {
			return nil
		}
		list = append(list, one)
	}
	apps := map[string]string{}
	for _, a := range list {
		if a.Name == "" || a.AppID == "" || skipApp(a.Name) {
			continue
		}
		if _, dup := apps[a.Name]; !dup {
			apps[a.Name] = `shell:AppsFolder\` + a.AppID
		}
	}
	return apps
}

// shortcutApps is the Start menu's shortcut files, for everybody and for this user, by name.
func shortcutApps() map[string]string {
	apps := map[string]string{}
	for _, root := range []string{
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`),
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`),
	} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".lnk") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			if skipApp(name) {
				return nil
			}
			if _, dup := apps[name]; !dup {
				apps[name] = p
			}
			return nil
		})
	}
	return apps
}

func shellOpen(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func openURL(u string) error    { return shellOpen(u) }
func openApp(path string) error { return shellOpen(path) }

// runScript starts a command from the script list and doesn't wait for it, without a console window
// of its own.
func runScript(command string) error {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/d /s /c "` + command + `"`, HideWindow: true}
	return start(cmd)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// setStartup starts the agent, with args, when this user signs in, or stops doing so.
func setStartup(on bool, args []string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("TECHO5 Deck"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := syscall.EscapeArg(exe)
	for _, a := range args {
		cmd += " " + syscall.EscapeArg(a)
	}
	return k.SetStringValue("TECHO5 Deck", cmd)
}
