//go:build !linux

package fastjpeg

import (
	"os"
	"os/exec"
)

func widen(*os.File) {}

func confine(cmd *exec.Cmd) { cmd.Env = []string{} }
