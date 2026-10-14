//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// updateOverlay draws the system update dialog last, above the page currently on screen.
func (r *renderer) updateOverlay(s scene) {
	if !s.updateActive && s.updateMessage == "" {
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
	title := "System update"
	if s.updateMessage != "" {
		title = "Update failed"
	}

	r.text(
		r.title,
		title,
		(r.w-r.width(r.title, title))/2,
		card.Min.Y+r.s(72),
		amber,
	)

	if s.updateTarget != "" {
		target := "Version " + s.updateTarget
		target = r.fit(r.body, target, card.Dx()-2*padding)
		r.text(
			r.body,
			target,
			(r.w-r.width(r.body, target))/2,
			card.Min.Y+r.s(130),
			cream,
		)
	}

	// A failed update stays visible briefly so the error can be read without trapping
	// the user on a permanent dialog.
	if s.updateMessage != "" {
		message := r.fit(r.small, s.updateMessage, card.Dx()-2*padding)
		r.text(
			r.small,
			message,
			(r.w-r.width(r.small, message))/2,
			card.Min.Y+r.s(205),
			cream,
		)

		retry := "Retry from Settings > General > Updates"
		retry = r.fit(r.small, retry, card.Dx()-2*padding)
		r.text(
			r.small,
			retry,
			(r.w-r.width(r.small, retry))/2,
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
		status = "Installing update..."
	case "restarting":
		status = "Restarting..."
	default:
		status = fmt.Sprintf("Downloading update · %d%%", int(progress*100+0.5))
	}

	status = r.fit(r.body, status, card.Dx()-2*padding)
	r.text(
		r.body,
		status,
		(r.w-r.width(r.body, status))/2,
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

	if s.updatePhase == "installing" || s.updatePhase == "restarting" {
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

	hint := "Keep the device powered on"
	r.text(
		r.small,
		hint,
		(r.w-r.width(r.small, hint))/2,
		card.Min.Y+r.s(290),
		dim,
	)
}
