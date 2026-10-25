//go:build !panel && !(dot && donut)

package speaker

// formatFor is the board's one format, whatever the output.
func formatFor(Output) cardFormat {
	return cardFormat{rate: hwRate, bits: hwBits, open: openSequence}
}
