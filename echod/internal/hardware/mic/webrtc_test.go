package mic

import (
	"bufio"
	"os/exec"
	"testing"
	"time"
)

func TestProcessGivesUpOnAHelperThatStopsAnswering(t *testing.T) {
	// sleep takes the block into its pipe and never writes a word back, which is what a wedged
	// helper looks like from here.
	cmd := exec.Command("sleep", "60")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep to stand in for the helper:", err)
	}
	e := &external{cmd: cmd, in: in, out: bufio.NewReader(out)}
	go func() { _ = cmd.Wait() }()
	defer e.Close()

	done := make(chan error, 1)
	go func() {
		_, err := e.Process(make([]int16, 320), make([]int16, 320))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Process returned a block the helper never sent")
		}
		if e.Healthy() {
			t.Error("a helper that was killed for stalling still reports healthy")
		}
	case <-time.After(helperStall + 5*time.Second):
		t.Fatal("Process is still waiting on a helper that will never answer")
	}
}
