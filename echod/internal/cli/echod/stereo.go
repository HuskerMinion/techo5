package echod

import (
	"fmt"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/spf13/cobra"
)

// newStereoCmd provides the sole Stage 1 opt-in until Home Assistant and the
// web setup UI are implemented by the maintainer. It persists the setting,
// but intentionally does not restart a running service.
func newStereoCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stereo [on|off]",
		Short: "Save athena internal-speaker stereo mode (mono by default)",
		Long:  "Saves left/right speaker mode in TECHO5 state.json. Use without arguments to inspect. Stop/restart the agent to apply the changed setting; other boards remain mono.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "athena stereo enabled: %t (takes effect when echod starts)\n", config.Get().Speaker.Stereo && layout.Board == "athena")
				return nil
			}
			if layout.Board != "athena" {
				return fmt.Errorf("stereo internal speakers are supported on athena only")
			}
			var want bool
			switch args[0] {
			case "on":
				want = true
			case "off":
				want = false
			default:
				return fmt.Errorf("expected on or off")
			}
			if err := config.Set().Speaker().Stereo(want); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "athena stereo saved: %t; restart echod to apply\n", want)
			return nil
		},
	}
	return c
}
