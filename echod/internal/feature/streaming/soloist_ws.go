package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/gorilla/websocket"
)

func soloistEndpoint() string {
	b, err := os.ReadFile(filepath.Join(soloistData(), "ws.port"))
	if err != nil {
		return ""
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || p < 1 || p > 65535 {
		return ""
	}
	// Never trust an address from writable session state to direct commands off-device.
	return "ws://127.0.0.1:" + strconv.Itoa(p)
}

type soloistCover struct {
	URL  string `json:"url"`
	Size string `json:"size"`
}

func soloistCoverURL(covers []soloistCover) string {
	best, rank := "", -1
	for _, cover := range covers {
		if !strings.HasPrefix(cover.URL, "https://") {
			continue
		}
		r := map[string]int{"small": 1, "default": 2, "large": 3, "xlarge": 4}[strings.ToLower(cover.Size)]
		if r > rank {
			best, rank = cover.URL, r
		}
	}
	return best
}

type soloistEntity struct {
	URI         string `json:"uri"`
	Decorations struct {
		Identity struct {
			Name string `json:"name"`
		} `json:"identity"`
		Visual struct {
			Cover []soloistCover `json:"cover"`
		} `json:"visual_identity"`
		Parent struct {
			Entity *soloistEntity `json:"entity"`
		} `json:"parent"`
		Creators []struct {
			Entity *soloistEntity `json:"entity"`
		} `json:"creators"`
		Playback struct {
			DurationMS int64 `json:"duration_ms"`
		} `json:"playback"`
	} `json:"decorations"`
}

type soloistEvent struct {
	Type     string           `json:"type"`
	Status   string           `json:"status"`
	LoggedIn bool             `json:"logged_in"`
	Item     soloistEntity    `json:"item"`
	Command  string           `json:"command"`
	Position *soloistPosition `json:"position"`
	Volume   *int             `json:"volume"`
}

func soloistTrack(e soloistEntity) {
	d := e.Decorations
	var artists []string
	for _, c := range d.Creators {
		if c.Entity != nil {
			artists = append(artists, c.Entity.Decorations.Identity.Name)
		}
	}
	album, cover := "", ""
	if d.Parent.Entity != nil {
		album = d.Parent.Entity.Decorations.Identity.Name
	}
	cover = soloistCoverURL(d.Visual.Cover)
	if cover == "" && d.Parent.Entity != nil {
		cover = soloistCoverURL(d.Parent.Entity.Decorations.Visual.Cover)
	}
	media.Get().SetReceivedTrack(home.SpotifyName, d.Identity.Name, strings.Join(artists, ", "), album)
	home.ReceivedArt(home.SpotifyName, cover)
}

func followSoloist(ctx context.Context, playing *atomic.Bool, starts *startCount, unhealthy chan<- struct{}, gain *level, raw *os.File) {
	for ctx.Err() == nil {
		endpoint := soloistEndpoint()
		if endpoint != "" {
			d := websocket.Dialer{HandshakeTimeout: 2 * time.Second, Proxy: nil}
			c, resp, err := d.DialContext(ctx, endpoint, http.Header{})
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			if err == nil {
				timing := soloistProgress{}
				var writeMu sync.Mutex
				levels := make(chan int, 1)
				one := newSpotifySync()
				one.device, one.set, one.most = media.Get().Volume, media.Get().Set, media.Get().CapRaise
				one.write = func(v int) bool {
					if ctx.Err() != nil {
						return false
					}
					percent := (v*100 + spotifyFull/2) / spotifyFull
					select {
					case levels <- percent:
						return true
					default:
						return false
					}
				}
				one.started()
				stopLevel := media.Get().OnLevel.Listen(one.levelChanged)
				loggedIn, authSeen := false, false
				var connected time.Time
				queryState := func() {
					writeMu.Lock()
					defer writeMu.Unlock()
					_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
					_ = c.WriteJSON(map[string]string{"type": "command", "command": "get_state"})
				}
				stop := context.AfterFunc(ctx, func() { c.Close() })
				connection, disconnect := context.WithCancel(ctx)
				writerDone := make(chan struct{})
				go func() {
					defer close(writerDone)
					ticker := time.NewTicker(10 * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-connection.Done():
							return
						case percent := <-levels:
							writeMu.Lock()
							_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
							err := c.WriteJSON(map[string]any{"type": "command", "command": "set_volume", "volume": percent})
							writeMu.Unlock()
							if err != nil {
								c.Close()
								return
							}
						case <-ticker.C:
							writeMu.Lock()
							_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
							err := c.WriteJSON(map[string]string{"type": "command", "command": "get_auth_state"})
							writeMu.Unlock()
							if err != nil {
								c.Close()
								return
							}
						}
					}
				}()
				c.SetReadLimit(1 << 20)
				for ctx.Err() == nil {
					_ = c.SetReadDeadline(time.Now().Add(25 * time.Second))
					var event soloistEvent
					if c.ReadJSON(&event) != nil {
						break
					}
					timing.update(event, time.Now())
					if event.Volume != nil && *event.Volume >= 0 && *event.Volume <= 100 {
						gain.set(soloistVolumeGain(*event.Volume), pipeQueuedBytes(raw))
						one.heard(soloistVolumeValue(*event.Volume), !connected.IsZero() && time.Since(connected) < connectQuiet)
					}
					switch event.Type {
					case "auth_state":
						if !authSeen || loggedIn != event.LoggedIn {
							one.session(event.LoggedIn)
							if event.LoggedIn {
								connected = time.Now()
							}
						}
						loggedIn, authSeen = event.LoggedIn, true
						if event.LoggedIn {
							setSoloistStatus("Connected to Spotify")
						} else {
							setSoloistStatus("Soloist running; select this device in Spotify to pair")
						}
					case "playback_state", "playback_changed":
						on := event.Status == "playing"
						if on && !playing.Swap(on) {
							starts.start()
						} else {
							playing.Store(on)
						}
						media.Get().ReceiverPaused(home.SpotifyName, !on)
						if event.Type == "playback_state" {
							soloistTrack(event.Item)
						}
					case "track_changed":
						soloistTrack(event.Item)
						queryState() // obtain a fresh title/duration/position snapshot after a skip
					}
				}
				stop()
				stopLevel()
				one.started()
				disconnect()
				<-writerDone
				c.Close()
				playing.Store(false)
				media.ClearSourcePosition(home.SpotifyName)
				if ctx.Err() == nil {
					select {
					case unhealthy <- struct{}{}:
					default:
					}
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func soloistControl(t media.Transport) func() bool {
	if !config.Get().Streaming.SpotifySoloist {
		return func() bool { return false }
	}
	commands := map[media.Transport]string{media.TransportPlay: "play", media.TransportPause: "pause",
		media.TransportNext: "skip_next", media.TransportPrevious: "skip_prev"}
	command := commands[t]
	return func() bool {
		if command == "" {
			return false
		}
		return sendSoloistCommand(command)
	}
}

func sendSoloistCommand(command string) bool {
	endpoint := soloistEndpoint()
	if endpoint == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := websocket.Dialer{HandshakeTimeout: 2 * time.Second, Proxy: nil}
	c, resp, err := d.DialContext(ctx, endpoint, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetReadLimit(1 << 20)
	c.SetWriteDeadline(time.Now().Add(time.Second))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if c.WriteJSON(map[string]string{"type": "command", "command": command}) != nil {
		return false
	}
	for {
		var e soloistEvent
		_, b, err := c.ReadMessage()
		if err != nil {
			return false
		}
		if json.Unmarshal(b, &e) != nil {
			return false
		}
		if e.Type == "error" {
			return false
		}
		if e.Type == "command_result" && e.Command == command {
			return true
		}
	}
}
