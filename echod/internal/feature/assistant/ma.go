package assistant

import (
	"context"
	"errors"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// Playing through Music Assistant (config.MusicAssistant): music from its library, and any radio
// stream the device cannot decode itself, which the server converts for it. The server plays on this
// device as a Sendspin player known by the device's Sendspin ids (its key, or its factory MAC from
// before encryption), so nothing it plays is heard unless that player is connected; that is checked
// first, and what the server then does is checked after, so that the answer is what actually
// happened rather than what was asked for.

// maStartWait is how long something asked of Music Assistant has to start playing here before it
// counts as not having played. A variable so that a test need not wait for it.
var maStartWait = 20 * time.Second

// maPoll is how often the player is looked at meanwhile.
var maPoll = time.Second

// playerID is this device's player ids at the server (media.LibraryPlayerIDs); a variable for the tests.
var playerID = media.LibraryPlayerIDs

// maClient is the Music Assistant set up on this device and this device's player there.
func maClient() (musicassistant.Client, []string, error) {
	m := config.Get().MusicAssistant
	if !m.Set() {
		return musicassistant.Client{}, nil, errors.New("no music library is set up on this device")
	}
	player, err := playerID()
	if err != nil {
		return musicassistant.Client{}, nil, errors.New("this device does not know its own player id")
	}
	return musicassistant.Client{URL: m.URL, Token: m.Token}, player, nil
}

// playOnMA has Music Assistant play uri (one of its own, or a stream's address) on this device, and
// waits for it to be playing.
func playOnMA(ctx context.Context, uri string) error {
	c, player, err := maClient()
	if err != nil {
		return err
	}
	return c.PlayChecked(ctx, player, uri, maStartWait, maPoll, nil)
}
