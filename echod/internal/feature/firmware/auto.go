package firmware

import (
	"context"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// Installing by itself (config.Update.AutoInstall), for a device nobody is going to press Install on:
// one with no Home Assistant, in another home. Overnight, when an update's restart and the minute
// without the device are least noticed, and only while it is quiet: nothing playing, nothing ringing.
// A build that does not come up is rolled back by the slot trial as any update is.

const (
	// autoFrom and autoTo are the hours it may install in, local time.
	autoFrom, autoTo = 2, 5

	// autoLook is how often it looks, inside those hours and out.
	autoLook = 15 * time.Minute
)

func (u *Firmware) buildAuto() {
	u.auto = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "update_automatically",
			Name:     "Install updates automatically",
			Icon:     "mdi:update",
			Category: esphome.CategoryConfig,
		},
	}
	u.auto.OnCommand = func(on bool) {
		u.auto.Set(on)
		if err := config.Set().Update().AutoInstall(on); err != nil {
			slog.Error("saving automatic updates failed", "err", err)
		}
		slog.Info("updates install automatically", "on", on)
	}
}

// Restore shows the saved setting.
func (u *Firmware) Restore(c config.Config) { u.auto.Set(c.Update.AutoInstall) }

// SetAutoInstall is the setting, from the screen or the setup page.
func (u *Firmware) SetAutoInstall(on bool) { u.auto.OnCommand(on) }

// AutoInstall is whether updates install by themselves.
func (u *Firmware) AutoInstall() bool { return config.Get().Update.AutoInstall }

// autoLoop installs overnight. One try a night: a download that fails is tried again the next.
func (u *Firmware) autoLoop(ctx context.Context) {
	t := time.NewTicker(autoLook)
	defer t.Stop()
	var tried string // the night last tried, as a date
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		playing, _ := media.Get().Playing()
		busy := playing || ring.IsSounding() || update.Installing()
		if !autoDue(config.Get().Update.AutoInstall, now, tried, busy) {
			continue
		}
		tried = now.Format("2006-01-02")
		if !update.Published() {
			// A panel's app looks and installs in one go (askApp), and installs nothing that is not newer.
			slog.Info("asking the app to install an update by itself, overnight")
			u.Install(ctx)
			continue
		}
		u.Check(ctx)
		if v := u.Offered(); v != "" {
			slog.Info("installing an update by itself, overnight", "version", v)
			u.Install(ctx)
		}
	}
}

// autoDue is whether to look for an update and install it now: switched on, inside the night's hours,
// not tried already tonight, and nothing playing or ringing.
func autoDue(on bool, now time.Time, tried string, busy bool) bool {
	return on && !busy && now.Hour() >= autoFrom && now.Hour() < autoTo && tried != now.Format("2006-01-02")
}
