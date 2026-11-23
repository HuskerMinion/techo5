package echod

import (
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/HuskerMinion/techo5/echod/internal/android/logd"
	"github.com/HuskerMinion/techo5/echod/internal/boot"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
	"github.com/HuskerMinion/techo5/echod/internal/lib/profile"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run",
		Short: "Run the device agent",
		Long: "Plays the boot animation, then stays resident. echod is installed in place of\n" +
			"Amazon's ledcontroller service, so init starts it from on post-fs-data and\n" +
			"restarts it if it exits, and nothing else drives the ring.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := logd.NewHandler(layout.LogTag, cmd.ErrOrStderr())
			defer h.Close()
			slog.SetDefault(slog.New(h))
			// Every package has initialized by now: the panel's control line may hand on what the app
			// has said (panelctl.Ready). On a device that is not a panel nothing ever dialed it.
			panelctl.Ready()

			// init discards our stderr, so a panic would otherwise vanish and look like a silent
			// restart. Log it, then let it kill the process as it would have.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic", "panic", r, "stack", string(debug.Stack()))
					h.Close()
					panic(r)
				}
			}()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			signal.Ignore(syscall.SIGHUP)

			safe.Go("profile", func() { profile.Watch(ctx) })
			return boot.Run(ctx)
		},
	}

	return c
}
