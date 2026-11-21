//go:build spot

package display

// The Echo Spot has no cast receiver, so its settings have no Cast rows.

func castRows() []settingRow { return nil }

func (d *Display) castRowTap(id string) {}
