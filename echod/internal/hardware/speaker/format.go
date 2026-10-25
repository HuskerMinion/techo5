package speaker

// cardFormat is what the playback device is opened at: the card's rate, the width of the samples it is
// sent (32 is S24_LE, see widenS16), and the routes it needs on before it will open. Most boards have one
// for every output (formatFor); the Echo Dot 3rd gen has two, and the player opens the device again when
// the output moves between them (Run).
type cardFormat struct {
	rate, bits int
	open       []kctl
}

// same is whether g opens the device as f does, so that a change of output needs no new stream.
func (f cardFormat) same(g cardFormat) bool { return f.rate == g.rate && f.bits == g.bits }
