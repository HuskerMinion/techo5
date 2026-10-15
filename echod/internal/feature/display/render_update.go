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

	var status string
	switch s.updatePhase {
	case "installing":
		status = locale.UpdateText(lang, locale.UpdateInstalling)
	case "countdown-3":
		status = fmt.Sprintf(locale.UpdateText(lang, locale.UpdateRebootingIn), 3)
	case "countdown-2":
		status = fmt.Sprintf(locale.UpdateText(lang, locale.UpdateRebootingIn), 2)
	case "countdown-1":
		status = fmt.Sprintf(locale.UpdateText(lang, locale.UpdateRebootingIn), 1)
	case "restarting":
		status = locale.UpdateText(lang, locale.UpdateRebooting)
	default:
		status = fmt.Sprintf(locale.UpdateText(lang, locale.UpdateDownloading), int(progress*100+0.5))
	}

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

	indeterminate := false
	switch s.updatePhase {
	case "installing", "restarting", "countdown-3", "countdown-2", "countdown-1":
		indeterminate = true
	}
	if indeterminate {
		// slotctl does not report progress while writing the inactive slot.
		// Animate a segment instead of showing a misleading percentage.
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
