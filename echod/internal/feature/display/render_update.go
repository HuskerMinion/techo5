//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// updateOverlay draws the system update dialog last, above the page currently on screen.
func (r *renderer) updateOverlay(s scene) {
	if !s.updateActive {
		return
	}
	// A ring or a call is never covered: a tap has to be able to stop the ring or work the call, and
	// its page is how. The dialog comes back once the ring is over.
	if s.ring.any() || s.call.Phase != phone.Idle {
		return
	}

	// Dim the existing page so the update is clearly the focus.
	draw.Draw(
		r.dst,
		r.dst.Rect,
		image.NewUniform(color.RGBA{R: 0, G: 0, B: 0, A: 155}),
		image.Point{},
		draw.Over,
	)

	cardW := r.s(760)
	if available := r.w - r.s(64); cardW > available {
		cardW = available
	}
	if cardW < r.s(360) {
		cardW = r.w - r.s(24)
	}

	cardH := r.s(340)
	if available := r.h - r.s(24); cardH > available {
		cardH = available
	}

	left := (r.w - cardW) / 2
	top := (r.h - cardH) / 2
	card := image.Rect(left, top, left+cardW, top+cardH)
	radius := float64(r.s(28))

	r.roundShadow(card, radius, float64(r.s(16)), r.s(6), 0.55)
	r.roundFill(card, radius, walnut, walnut)
	r.roundStroke(card, radius, float64(r.s(2)), ember)

	padding := r.s(42)
	faces := r.faces()
	titleFace := faces.header
	versionFace := faces.labelBold
	statusFace := faces.label
	detailFace := faces.sub

	lang := screenLang()
	title := locale.UpdateText(lang, locale.UpdateSystemTitle)
	if s.updateMessage != "" {
		title = locale.UpdateText(lang, locale.UpdateFailedTitle)
	}

	r.text(
		titleFace,
		title,
		(r.w-r.width(titleFace, title))/2,
		card.Min.Y+r.s(72),
		amber,
	)

	if s.updateTarget != "" {
		oldVersion := "v" + strings.TrimPrefix(strings.TrimSpace(layout.Version), "v")
		newVersion := "v" + strings.TrimPrefix(strings.TrimSpace(s.updateTarget), "v")
		target := oldVersion + " → " + newVersion
		target = r.fit(versionFace, target, card.Dx()-2*padding)
		r.text(
			versionFace,
			target,
			(r.w-r.width(versionFace, target))/2,
			card.Min.Y+r.s(130),
			cream,
		)
	}

	// A failed update stays visible briefly so the error can be read without trapping
	// the user on a permanent dialog.
	if s.updateMessage != "" {
		message := r.fit(statusFace, s.updateMessage, card.Dx()-2*padding)
		r.text(
			statusFace,
			message,
			(r.w-r.width(statusFace, message))/2,
			card.Min.Y+r.s(205),
			cream,
		)

		retry := locale.UpdateText(lang, locale.UpdateRetryHint)
		retry = r.fit(detailFace, retry, card.Dx()-2*padding)
		r.text(
			detailFace,
			retry,
			(r.w-r.width(detailFace, retry))/2,
			card.Min.Y+r.s(270),
			dim,
		)
		return
	}

	progress := s.updateProgress
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	status := updateStatus(lang, s.updatePhase, progress)
	status = r.fit(statusFace, status, card.Dx()-2*padding)
	r.text(
		statusFace,
		status,
		(r.w-r.width(statusFace, status))/2,
		card.Min.Y+r.s(190),
		cream,
	)

	bar := image.Rect(
		card.Min.X+padding,
		card.Min.Y+r.s(225),
		card.Max.X-padding,
		card.Min.Y+r.s(243),
	)
	barRadius := float64(bar.Dy()) / 2
	r.roundFill(bar, barRadius, ember, ember)

	if updateBarSpins(s.updatePhase) {
		// The step reports no progress of its own (slotctl writes the inactive slot in silence). Animate
		// a segment rather than leaving a bar stuck at the end of the download.
		segmentW := bar.Dx() / 3
		if segmentW > r.s(180) {
			segmentW = r.s(180)
		}
		if segmentW < r.s(1) {
			segmentW = 1
		}
		if segmentW > bar.Dx() {
			segmentW = bar.Dx()
		}

		travel := bar.Dx() - segmentW
		offset := 0
		if travel > 0 {
			offset = int((s.now.UnixMilli() / 20) % int64(travel+1))
		}

		segment := image.Rect(
			bar.Min.X+offset,
			bar.Min.Y,
			bar.Min.X+offset+segmentW,
			bar.Max.Y,
		)
		r.roundFill(segment, barRadius, amber, amber)
	} else {
		fillW := int(float64(bar.Dx()) * float64(progress))
		if fillW > 0 {
			fill := image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fillW, bar.Max.Y)
			r.roundFill(fill, barRadius, amber, amber)
		}
	}

	hint := locale.UpdateText(lang, locale.UpdatePowerHint)
	r.text(
		detailFace,
		hint,
		(r.w-r.width(detailFace, hint))/2,
		card.Min.Y+r.s(290),
		dim,
	)
}

// updateStatus is the line under the title: the step the install is in. The countdown and the step it
// counts to are worded for what the device actually does — a slot device reboots into the rootfs it
// has unpacked, while a binary swap only has this process replaced — so the screen does not call a
// restart a reboot. Anything else is a download, and the number is how far it has got.
func updateStatus(lang, phase string, progress float32) string {
	plain := func(key string) string { return locale.UpdateText(lang, key) }
	in := func(key string, seconds int) string { return fmt.Sprintf(locale.UpdateText(lang, key), seconds) }

	switch phase {
	case "downloading":
		return fmt.Sprintf(plain(locale.UpdateDownloading), int(progress*100+0.5))
	case "installing":
		return plain(locale.UpdateInstalling)
	case "rebooting-in-3":
		return in(locale.UpdateRebootingIn, 3)
	case "rebooting-in-2":
		return in(locale.UpdateRebootingIn, 2)
	case "rebooting-in-1":
		return in(locale.UpdateRebootingIn, 1)
	case "rebooting":
		return plain(locale.UpdateRebooting)
	case "restarting-in-3":
		return in(locale.UpdateRestartingIn, 3)
	case "restarting-in-2":
		return in(locale.UpdateRestartingIn, 2)
	case "restarting-in-1":
		return in(locale.UpdateRestartingIn, 1)
	case "restarting":
		return plain(locale.UpdateRestarting)
	}
	// Nothing named yet: an install whose download has just started.
	return fmt.Sprintf(plain(locale.UpdateDownloading), int(progress*100+0.5))
}

// updateBarSpins is whether the bar has no percentage to show: the steps that report no progress of
// their own — unpacking the rootfs into the other slot, the countdown, and the restart or reboot
// after it — rather than a download the bar can measure.
func updateBarSpins(phase string) bool {
	switch phase {
	case "installing", "rebooting", "restarting",
		"rebooting-in-3", "rebooting-in-2", "rebooting-in-1",
		"restarting-in-3", "restarting-in-2", "restarting-in-1":
		return true
	}
	return false
}
