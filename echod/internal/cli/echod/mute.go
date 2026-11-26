package echod

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/privacy"
)

func newMuteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "mute [on|off|toggle]",
		Short: "Read or set the hardware microphone mute",
		Long: "Cuts the microphones the way this device allows: MTK pin 87 (gpio444) where that line\n" +
			"is free, and the keypad driver's privacy interface where it is not. With no argument,\n" +
			"reports the current state.\n\n" +
			"The cut is physical: muted capture measures below the room noise floor, not merely\n" +
			"below speech. It needs none of Amazon's services, so it keeps working after\n" +
			"de-Amazoning — and a muted unit stays recoverable, which it would not be if we\n" +
			"depended on their handler.\n\n" +
			"It is still a software-controlled switch rather than a physical interlock: whatever\n" +
			"can mute can also unmute.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := privacy.Microphone()
			if err != nil {
				return err
			}

			// Where part of the mute is the running daemon's own (the Spot's and a panel's software
			// cut, the software mute beside the latch on the 2nd gen Show 8 on TECHO5's kernel), this
			// process has only its own copy of it: setting it would move the hardware behind the
			// daemon's back, or nothing at all, while Home Assistant and the screen said otherwise.
			if privacy.HeldByTheDaemon(m) {
				if len(args) == 1 {
					return errors.New("on this device the microphone mute is held by the running daemon: " +
						"set it from Home Assistant, the screen or the mute button")
				}
				return daemonHeldMute(cmd.OutOrStdout(), m)
			}

			if len(args) == 1 {
				switch args[0] {
				case "on", "true", "1":
					err = m.Set(true)
				case "off", "false", "0":
					err = m.Set(false)
				case "toggle":
					_, err = m.Toggle()
				default:
					return fmt.Errorf("expected on, off or toggle; got %q", args[0])
				}
				if err != nil {
					return err
				}
			}

			muted, err := m.Get()
			if err != nil {
				return err
			}
			state := "unmuted (microphones live)"
			if muted {
				state = "MUTED (microphones cut)"
			}
			fmt.Fprintln(cmd.OutOrStdout(), state)
			return nil
		},
	}
	return c
}

// daemonHeldMute reports what a process other than the daemon can see of a mute the daemon holds: the
// button's latch, and the converter's channels, where the software mute is there. Where it is not
// (the Spot, a panel), the daemon's state is not visible from here, and it says so rather than guess.
func daemonHeldMute(out io.Writer, m privacy.Mute) error {
	latch, ok := m.(interface{ Latched() (bool, error) })
	cut, known, err := privacy.CodecCut()
	if !ok || !known {
		fmt.Fprintln(out, "held by the running daemon: Home Assistant and the screen show it")
		return err
	}
	if err != nil {
		return err
	}
	latched, err := latch.Latched()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "button latch: %s\nsoftware mute (converter channels off): %s\n", onOff(latched), onOff(cut))
	state := "unmuted (microphones live)"
	if latched || cut {
		state = "MUTED (microphones cut)"
	}
	fmt.Fprintln(out, state)
	return nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
