//go:build panel

package speaker

// formatFor is the app's one format: the stream as it comes, nothing to route first.
func formatFor(Output) cardFormat { return cardFormat{rate: hwRate, bits: hwBits} }
