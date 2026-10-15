//go:build linux

// Package btaudio plays through Bluetooth earbuds or a speaker. bluetoothd owns the radio and the
// bonds, bluez-alsa owns the A2DP stream; this feature pairs and connects the device, and while it
// is connected hands the speaker's audio to bluez-alsa instead of the codec.
//
// Pairing is a mode the user turns on — from the screen, from Home Assistant — and it ends on its
// own: while it is on the radio scans, and scanning shares the antenna with Wi-Fi. What the scan
// finds is listed on the screen; a tap pairs, trusts and connects it. A trusted device reconnects
// on its own when it is switched on, and the daemon tries the last one once at start-up.
//
// Phone pairing is the other way round: a phone plays to the device. It makes the device findable
// and pairable for a while with no scan and no pick, so a phone (a new one, or one paired before)
// connects from its own Bluetooth settings, and it ends once a phone's audio is up. Its switch
// stays on while a phone is connected, and turning it off disconnects the phone, so one switch
// connects and disconnects. A button calls the paired phones back, for a phone that left and came
// home, since a phone may not look for the device on its own. Anyone nearby can pair while phone
// pairing is on, so another button forgets every phone (and drops it), leaving the speakers and
// earbuds the device plays to.
//
// To Home Assistant: a switch for pairing mode, a "Bluetooth phone" switch (on while phone pairing
// is on or a phone is connected; off disconnects it), a sensor saying what is connected (a phone
// too), buttons to reconnect or disconnect the remembered device, and buttons to call the phones
// back or forget them.
package btaudio

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	if layout.OnAndroid() {
		return // Android's own stack owns the radio there
	}
	component.Register(component.Device, Get(), component.Order(70),
		component.Supervise(service.Restart(2*time.Second, 30*time.Second)))
}

const (
	// pairingFor is how long pairing mode stays on by itself.
	pairingFor = 3 * time.Minute

	// phoneFor is how long phone pairing stays on by itself.
	phoneFor = 2 * time.Minute

	// connectTimeout bounds one attempt; earbuds that are in their case never answer.
	connectTimeout = 20 * time.Second

	notConnected = "Not connected"
)

// onPaired is told when pairing mode has done its job: a phone paired with the device and connected,
// or a device it paired out to connected. A screen shows that; the Echo Dot sounds it (button_dot.go).
var onPaired = func() {}

// Device is one entry on the screen's list.
type Device struct {
	Address   string
	Name      string
	RSSI      int
	Paired    bool
	Connected bool
	Busy      bool // being paired or connected right now
}

// State is what the screen shows.
type State struct {
	Available  bool // bluetoothd is there
	Pairing    bool
	Phone      bool     // phone pairing is on
	Devices    []Device // while pairing: what the scan found that could play audio
	Connected  string   // the connected audio device's name, empty for none
	Remembered string   // the last device's name, for "waiting for …"
	Status     string   // one line: what just happened
}

func sameState(a, b State) bool {
	return a.Available == b.Available && a.Pairing == b.Pairing && a.Phone == b.Phone &&
		a.Connected == b.Connected && a.Remembered == b.Remembered && a.Status == b.Status &&
		slices.Equal(a.Devices, b.Devices)
}

type Feature struct {
	pairing    *esphome.Switch
	phonePair  *esphome.Switch
	status     *esphome.TextSensor
	reconnect  *esphome.Button
	disconnect *esphome.Button
	callPhone  *esphome.Button
	forget     *esphome.Button
	forgetOne  *esphome.Button // the earbuds or speaker the device plays to (Forget)

	// Changed fires when State does; listeners must not block.
	Changed hook.Hook[State]

	mu      sync.Mutex
	adapter *bluez.Adapter
	alsa    *bluealsa.Client
	stream  *bluealsa.Stream
	state   State
	shown   State // the last State sent on Changed
	busy    map[string]bool
	pairOff *time.Timer
	// modes serializes turning pairing mode or phone pairing on and off, so both are never on.
	modes sync.Mutex
	// phone is phone pairing: findable and pairable like pairing mode, with no scan and no pick.
	phone    bool
	phoneOff *time.Timer
	// phoneGen counts phone pairing windows, so a phoneOff already waiting when it was stopped
	// cannot close the window opened after it.
	phoneGen int
	// phoneAudio is the phones whose audio was up at the last look, by D-Bus path: a phone joins
	// phone pairing when its audio comes up, not because it was already playing when it began.
	phoneAudio map[string]bool
	// phoneLinked is whether a phone was connected at the last look; the phone switch shows it too,
	// except while hangingUp: turned off, the switch shows off at once, not when the phone has gone.
	phoneLinked bool
	hangingUp   bool
	// phoneShow serializes working out and publishing the phone switch, so a stale look never lands
	// after a newer one.
	phoneShow sync.Mutex
	// On a device with no screen to choose on (autoPick), pairing mode connects the strongest audio
	// device it hears once it has listened a while; tried keeps a refusal from being picked again.
	pairingSince time.Time
	tried        map[string]bool
	picking      bool
	// incoming is a remote (a phone) that paired with the device during this pairing mode, by D-Bus
	// path. It answers the guess pairing mode makes: something wants to play here, so no speaker is
	// picked, and pairing mode ends once it has connected.
	incoming string
	// received is whether each receivable stream was running at the last look, so a remote that starts
	// playing takes the speaker once, and whatever is started after it is not taken back.
	received map[string]bool
	// displaced is the remotes, by the name their audio plays under, that another remote took the
	// speaker from while they were still sending: once nothing is playing, one still sending takes it
	// back (refresh).
	displaced map[string]bool
	// pausedAway is the phones paused for playing to nobody, by item, while they go on sending
	// (pauseAway): each is asked once.
	pausedAway map[string]bool
	// kept ends a phone's track left paused too long (keep), one timer for each phone by item.
	kept map[string]*time.Timer
	// levels is each received stream's level as last seen or set, by PCM path: a phone's volume slider,
	// which is the device's volume while it plays here (phoneSlider, tellPhone).
	levels    map[string]int
	levelOnce sync.Once
	// ours is, for each received stream whose level was set from here, until when a level read from it
	// may be the phone saying that one back rounded to its own steps rather than its slider moving
	// (sliderMove): a short while, since most phones say nothing back, and a press after it always
	// counts.
	ours map[string]time.Time
	// levelTo hands the device's volume step to the one goroutine that tells the phone (tellPhone), the
	// latest only, so two steps in quick succession reach the phone in order.
	levelTo chan int
	// hushed is the devices this side is dropping and connecting again (the remake attach does), by
	// lower-case address, and until when: no tone for either (linkTones).
	hushed map[string]time.Time
	// linked is the audio devices connected at the last look, by D-Bus path: earbuds, speakers and
	// phones, for the tones a connect and a disconnect play (linkTones). linkSeen is whether there has
	// been a look since bluetooth came up, before which there is nothing to compare with.
	linked   map[string]bool
	linkSeen bool
	poke     chan struct{}

	// refusals counts stream opens bluetoothd refused for the current connection; see attach.
	refusals int
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Feature {
	f := &Feature{
		pairing: &esphome.Switch{Base: esphome.Base{
			ObjectID: "bluetooth_pairing", Name: "Bluetooth pairing", Icon: "mdi:bluetooth-settings",
			Category: esphome.CategoryConfig,
		}},
		phonePair: &esphome.Switch{Base: esphome.Base{
			ObjectID: "bluetooth_phone_pairing", Name: "Bluetooth phone", Icon: "mdi:cellphone-sound",
			Category: esphome.CategoryConfig,
		}},
		status: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: "bluetooth_audio", Name: "Bluetooth audio", Icon: "mdi:headphones",
		}},
		reconnect: &esphome.Button{Base: esphome.Base{
			ObjectID: "bluetooth_reconnect", Name: "Bluetooth reconnect", Icon: "mdi:bluetooth-connect",
			Category: esphome.CategoryConfig,
		}},
		disconnect: &esphome.Button{Base: esphome.Base{
			ObjectID: "bluetooth_disconnect", Name: "Bluetooth disconnect", Icon: "mdi:bluetooth-off",
			Category: esphome.CategoryConfig,
		}},
		callPhone: &esphome.Button{Base: esphome.Base{
			ObjectID: "bluetooth_reconnect_phone", Name: "Bluetooth reconnect phone", Icon: "mdi:cellphone-link",
			Category: esphome.CategoryConfig,
		}},
		forget: &esphome.Button{Base: esphome.Base{
			ObjectID: "bluetooth_forget_phones", Name: "Bluetooth forget phones", Icon: "mdi:cellphone-remove",
			Category: esphome.CategoryConfig,
		}},
		forgetOne: &esphome.Button{Base: esphome.Base{
			ObjectID: "bluetooth_forget", Name: "Bluetooth forget", Icon: "mdi:headphones-off",
			Category: esphome.CategoryConfig,
		}},
		busy:       map[string]bool{},
		received:   map[string]bool{},
		displaced:  map[string]bool{},
		pausedAway: map[string]bool{},
		kept:       map[string]*time.Timer{},
		levels:     map[string]int{},
		ours:       map[string]time.Time{},
		hushed:     map[string]time.Time{},
		levelTo:    make(chan int, 1),
		linked:     map[string]bool{},
		phoneAudio: map[string]bool{},
		poke:       make(chan struct{}, 1),
		state:      State{Status: notConnected},
	}
	f.pairing.OnCommand = func(on bool) { f.SetPairing(on) }
	f.phonePair.OnCommand = func(on bool) {
		f.mu.Lock()
		f.hangingUp = !on
		f.mu.Unlock()
		f.SetPhonePairing(on)
		if !on {
			safe.Go("bluetooth phone disconnect", f.disconnectPhones)
		}
	}
	f.reconnect.OnPress = func() { safe.Go("bluetooth reconnect", f.Reconnect) }
	f.disconnect.OnPress = func() { safe.Go("bluetooth disconnect", f.Disconnect) }
	f.callPhone.OnPress = func() { safe.Go("bluetooth reconnect phone", f.ReconnectPhone) }
	f.forget.OnPress = func() { safe.Go("bluetooth forget phones", f.ForgetPhones) }
	f.forgetOne.OnPress = func() { safe.Go("bluetooth forget", f.Forget) }
	f.status.Set(notConnected)
	return f
}

func (f *Feature) Name() string { return "bluetooth audio" }

func (f *Feature) Entities() []esphome.Entity {
	return []esphome.Entity{
		f.pairing, f.phonePair, f.status, f.reconnect, f.disconnect, f.forgetOne, f.callPhone, f.forget,
	}
}

// State is a copy of what the screen shows.
func (f *Feature) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state
	s.Devices = append([]Device(nil), f.state.Devices...)
	return s
}

func (f *Feature) wake() {
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

// Start connects to bluetoothd and bluez-alsa if they are there already. They can come up well after
// the daemon (on the Echo Dot the Bluetooth bring-up takes over a minute), and a service that fails
// to start is not started again, so their absence is not a failure: Run keeps looking. The daemon,
// and with it the voice satellite, does not wait for Bluetooth.
func (f *Feature) Start(ctx context.Context) error {
	if err := f.open(ctx); err != nil {
		slog.Info("bluetooth audio not up yet; looking again every few seconds", "err", err)
	}
	return nil
}

// open connects to bluetoothd and bluez-alsa.
func (f *Feature) open(ctx context.Context) error {
	a, err := bluez.Open(ctx)
	if err != nil {
		return err
	}
	if err := a.RegisterAgent(f.acceptsPairing, f.pairedIncoming, f.pairingCode); err != nil {
		a.Close()
		return err
	}
	al, err := bluealsa.Open(ctx)
	if err != nil {
		a.Close()
		return err
	}
	if name := config.Get().Device.Name; name != "" {
		if err := a.Alias(name); err != nil {
			slog.Warn("bluetooth alias", "err", err)
		}
	}
	// Not findable until someone asks: bluetoothd remembers discoverable across restarts.
	if err := a.Pairing(false); err != nil {
		slog.Warn("bluetooth: leaving pairing mode", "err", err)
	}
	_ = a.Discover(false)
	f.mu.Lock()
	f.adapter, f.alsa = a, al
	f.state.Available = true
	f.linkSeen = false // the devices already connected as bluetooth comes up connected before this
	f.state.Remembered = config.Get().Bluetooth.AudioName
	f.mu.Unlock()
	a.Changed.Listen(func(struct{}) { f.wake() })
	a.PlayerChanged.Listen(f.playerChanged)
	al.Changed.Listen(func(struct{}) { f.wake() })
	slog.Info("bluetooth up", "address", a.Address(), "devices", len(a.Devices()))
	return nil
}

func (f *Feature) Close() error {
	f.mu.Lock()
	a, al := f.adapter, f.alsa
	f.adapter, f.alsa = nil, nil
	f.state.Available = false
	f.mu.Unlock()
	f.detach()
	if al != nil {
		al.Close()
	}
	if a != nil {
		a.Close()
	}
	return nil
}

// Run follows the devices and streams: attaches the speaker to an A2DP stream when one appears,
// lets it go when it does, and keeps the screen's list current.
func (f *Feature) Run(ctx context.Context) error {
	reconnecting := false
	for {
		f.mu.Lock()
		up := f.adapter != nil
		f.mu.Unlock()
		if !up {
			if err := f.open(ctx); err == nil {
				up = true
			}
		}
		// The remembered device gets its chance once Bluetooth is there, however long that took.
		if up && !reconnecting {
			reconnecting = true
			safe.Go("bluetooth first connect", func() { f.tryRemembered(ctx) })
		}
		f.refresh()
		select {
		case <-ctx.Done():
			return nil
		case <-f.poke:
		case <-time.After(5 * time.Second):
		}
	}
}

// tryRemembered gives the last device one chance at start-up. Earbuds in their case do not answer
// and that is fine.
func (f *Feature) tryRemembered(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	addr := f.remembered()
	if addr == "" {
		return
	}
	// Earbuds that were just switched on refuse the first attempt while they settle, so a few
	// tries, spaced out; ones in their case never answer, and that is the end of it.
	for attempt := 1; attempt <= 3; attempt++ {
		f.connect(ctx, addr, false)
		if d, ok := f.device(addr); ok && d.Connected {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// device looks a device up by address.
func (f *Feature) device(addr string) (bluez.Device, bool) {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return bluez.Device{}, false
	}
	return a.Device(addr)
}

// remembered is the device to reach for: the one saved here, or failing that the first audio
// device bluetoothd holds a bond for — one paired from the console, or before this build.
func (f *Feature) remembered() string {
	if addr := config.Get().Bluetooth.Audio; addr != "" {
		return addr
	}
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return ""
	}
	for _, d := range a.Devices() {
		if d.Paired && d.AudioSink {
			return d.Address
		}
	}
	return ""
}

// refresh reconciles: the stream the speaker should be on, and what the screen should say.
func (f *Feature) refresh() {
	f.mu.Lock()
	a, al := f.adapter, f.alsa
	f.mu.Unlock()
	if a == nil || al == nil {
		return
	}

	devices := a.Devices()
	byPath := map[string]bluez.Device{}
	for _, d := range devices {
		byPath[string(d.Path)] = d
	}

	// The stream: the first playable A2DP PCM whose device is connected.
	var want *bluealsa.PCM
	var wantDev bluez.Device
	for _, p := range al.PCMs() {
		if !p.Playable() {
			continue
		}
		d, ok := byPath[string(p.Device)]
		if !ok || !d.Connected {
			continue
		}
		pc := p
		want, wantDev = &pc, d
		break
	}

	f.mu.Lock()
	cur := f.stream
	f.mu.Unlock()
	switch {
	case want == nil && cur != nil:
		f.detach()
	case want == nil:
		f.mu.Lock()
		f.refusals = 0 // whatever comes next is a new connection
		f.mu.Unlock()
	case want != nil && (cur == nil || cur.Path != want.Path):
		f.attach(al, *want, wantDev)
	}

	// A remote playing to the device (a phone using it as a speaker): the moment its stream starts, it
	// takes the speaker. Only on that edge, so Home Assistant media started afterwards is not taken
	// back while the phone keeps its transport open.
	var sending []received
	taken := map[string]bool{} // streams taken this look: their level is the one handed to them, not one read
	for _, p := range al.PCMs() {
		if !p.Receivable() {
			continue
		}
		d, ok := byPath[string(p.Device)]
		running := p.Running && ok && d.Connected
		f.mu.Lock()
		was := f.received[string(p.Path)]
		f.received[string(p.Path)] = running
		f.mu.Unlock()
		if !running {
			continue
		}
		r := received{pcm: p, dev: d, item: media.BluetoothPrefix + displayName(d)}
		sending = append(sending, r)
		if was {
			continue
		}
		holder := media.Get().Receiving()
		taken[string(p.Path)] = true
		if !f.receive(al, p, d) {
			// Tried again at the next look rather than never: the stream a remote paused and resumed
			// quickly can still be held by the track that was playing it until that track lets go.
			f.mu.Lock()
			f.received[string(p.Path)] = false
			f.mu.Unlock()
			continue
		}
		if holder != "" && holder != r.item {
			f.mu.Lock()
			f.displaced[holder] = true
			f.mu.Unlock()
		}
	}
	// A remote another took the speaker from, still sending once that one has finished and nothing is
	// playing, takes it back, as a speaker goes on playing the phone that is still playing to it. A
	// remote that went quiet, or was stopped here, was not displaced and stays let go.
	// Music Assistant (or any remote) taking the speaker is the room going its own way: no phone is owed
	// it afterward, as none is after a stop here (OnStop).
	m := media.Get()
	external := m.ExternalPlaying()
	playing, paused := m.Playing()
	f.mu.Lock()
	if othersHadIt(m.Receiving(), external) {
		clear(f.displaced)
	}
	back := takeBack(f.displaced, sending, speakerFree(playing, paused, external))
	f.mu.Unlock()
	if back != nil {
		slog.Info("bluetooth: a device still sending takes the speaker back", "device", displayName(back.dev))
		taken[string(back.pcm.Path)] = true
		f.receive(al, back.pcm, back.dev)
	}
	f.pauseAway(playingToNobody(sending, m.Receiving(), playing, paused, external))
	if cur := m.Receiving(); cur != "" {
		for _, r := range sending {
			if r.item == cur && !taken[string(r.pcm.Path)] {
				f.phoneSlider(r.pcm)
			}
		}
	}

	// Devices whose audio is up: they have a stream to receive, playing or not.
	audioUp := map[string]bool{}
	for _, p := range al.PCMs() {
		if p.Receivable() {
			audioUp[string(p.Device)] = true
		}
	}

	// The screen and Home Assistant.
	f.mu.Lock()
	st := &f.state
	st.Connected = ""
	if f.stream != nil {
		if d, ok := byPath[string(f.stream.Device)]; ok {
			st.Connected = displayName(d)
		}
	}
	st.Devices = st.Devices[:0]
	if st.Pairing {
		for _, d := range devices {
			if !d.AudioSink && d.Icon != "audio-headset" && d.Icon != "audio-headphones" && d.Icon != "audio-card" {
				continue
			}
			if d.Name == "" {
				continue
			}
			st.Devices = append(st.Devices, Device{
				Address: d.Address, Name: displayName(d), RSSI: int(d.RSSI),
				Paired: d.Paired, Connected: d.Connected, Busy: f.busy[strings.ToLower(d.Address)],
			})
		}
		sort.SliceStable(st.Devices, func(i, j int) bool { return st.Devices[i].RSSI > st.Devices[j].RSSI })
	}
	// The guess answered: a remote paired with the device and has connected, so pairing mode has done
	// its job. It is trusted so it can come back on its own.
	joined := ""
	if st.Pairing && f.incoming != "" {
		if d, ok := byPath[f.incoming]; ok && d.Paired && d.Connected {
			joined = d.Address
			f.incoming = ""
		}
	}
	// Phone pairing has done its job once a phone can play to the device: its audio has come up (its
	// A2DP transport, which comes up before anything plays). Every connected phone is looked at, so a
	// second one can join while the first stays connected.
	pl := lookAtPhones(devices, config.Get().Bluetooth.Audio, audioUp, f.phoneAudio, f.phone)
	phoneJoined, linked, phoneName := pl.joined, pl.linked, pl.name
	phoneGen := f.phoneGen
	if phoneJoined != "" {
		f.phone = false // once: another look before phone pairing ends must not join again
	}
	f.phoneAudio = pl.audio
	f.phoneLinked = linked
	tone := f.linkTones(devices, config.Get().Bluetooth.Audio)
	tone = holdForJoin(tone, phoneJoined)
	if !linked {
		f.hangingUp = false // the phones turned off have gone
	}
	pick := ""
	if autoPick && st.Pairing && f.incoming == "" && joined == "" && st.Connected == "" && !f.picking && time.Since(f.pairingSince) >= pickAfter {
		for _, d := range st.Devices {
			if !f.tried[strings.ToLower(d.Address)] && !d.Busy {
				pick = d.Address
				f.tried[strings.ToLower(pick)] = true
				f.picking = true
				break
			}
		}
	}
	text := notConnected
	switch {
	case st.Connected != "":
		text = st.Connected
	case phoneName != "":
		text = "Phone: " + phoneName
	}
	snapshot := *st
	snapshot.Devices = append([]Device(nil), st.Devices...)
	// A refresh runs on every Bluetooth signal and every few seconds besides; most change nothing,
	// and each Changed redraws a screen.
	fresh := !sameState(snapshot, f.shown)
	if fresh {
		f.shown = snapshot
	}
	f.mu.Unlock()

	if f.status.Get() != text {
		f.status.Set(text)
	}
	if tone != nil && !config.Get().Speaker.Quiet(time.Now()) && !inCall() {
		speaker.Sound().Chime(tone)
	}
	f.showPhone()
	if fresh {
		f.Changed.Emit(snapshot)
	}

	if joined != "" {
		slog.Info("bluetooth pairing: a device paired with this one and connected; pairing mode ends", "address", joined)
		if err := a.Trust(joined, true); err != nil {
			slog.Warn("bluetooth trust", "address", joined, "err", err)
		}
		safe.Go("bluetooth pairing done", func() {
			f.SetPairing(false)
			onPaired()
		})
	}
	goneFrom(devices)
	if phoneJoined != "" {
		slog.Info("bluetooth phone pairing: a phone connected; phone pairing ends", "address", phoneJoined)
		if err := a.Trust(phoneJoined, true); err != nil {
			slog.Warn("bluetooth trust", "address", phoneJoined, "err", err)
		}
		safe.Go("bluetooth phone pairing done", func() {
			// The window the phone joined in, not one opened since.
			f.modes.Lock()
			f.setPhonePairing(false, phoneGen)
			f.modes.Unlock()
			onPaired()
		})
	}
	if pick != "" {
		slog.Info("bluetooth pairing: connecting the strongest audio device heard", "address", pick)
		safe.Go("bluetooth auto pick", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*connectTimeout)
			defer cancel()
			f.connect(ctx, pick, true)
			f.mu.Lock()
			f.picking = false
			f.mu.Unlock()
			f.wake()
		})
	}
}

// receive plays what a remote is sending through the speaker, as a media track, and reports whether it
// is: a remote that paused and resumed before its track went quiet is still playing on that track.
func (f *Feature) receive(al *bluealsa.Client, p bluealsa.PCM, d bluez.Device) bool {
	item := media.BluetoothPrefix + displayName(d)
	if media.Get().Receiving() == item {
		return true
	}
	s, err := al.OpenRead(p)
	if err != nil {
		slog.Warn("bluetooth: opening a received stream", "device", displayName(d), "err", err)
		return false
	}
	slog.Info("bluetooth: playing what a device sends", "device", displayName(d), "codec", p.Codec, "rate", p.Rate, "channels", p.Channels)
	media.ReceiverControl(item, f.control(d.Path, displayName(d)))
	media.Get().PlayReceived(item, s, p.Rate, p.Channels)
	f.oneVolume(al, p)
	return true
}

// A phone playing here has one volume, the device's: its slider is the device's volume, as on any
// Bluetooth speaker. The phone sends its slider over AVRCP and leaves its audio unscaled; bluez-alsa
// scaling it by the slider as well put the two volumes on top of each other (half on the phone and
// 40% here came out much quieter than either). So bluez-alsa leaves the audio as it comes, the phone
// is handed the device's level as it starts playing here, a move of its slider sets the device's
// volume (phoneSlider), and the device's volume changing, by the buttons, Home Assistant or night
// volume, moves the slider (tellPhone). A phone that does not do AVRCP volume scales its own audio, and
// the level handed to it goes nowhere.

// levelFor is a phone's level, 0..bluealsa.VolumeMax, for the device's volume step.
func levelFor(step int) int {
	return (step*bluealsa.VolumeMax + media.VolumeSteps/2) / media.VolumeSteps
}

// stepFor is the device's volume step for a phone's level: the one levelFor takes back to it.
func stepFor(level int) int {
	return (level*media.VolumeSteps + bluealsa.VolumeMax/2) / bluealsa.VolumeMax
}

// oneVolume makes p's volume the device's: bluez-alsa stops scaling it, and the phone is handed the
// device's level.
func (f *Feature) oneVolume(al *bluealsa.Client, p bluealsa.PCM) {
	f.levelOnce.Do(func() {
		media.Get().OnLevel.Listen(f.handLevel)
		media.Get().OnStop.Listen(func(struct{}) {
			f.mu.Lock()
			clear(f.displaced) // stopped here: no phone takes the speaker back after that
			f.mu.Unlock()
		})
		safe.Go("bluetooth phone volume", func() {
			for step := range f.levelTo {
				f.tellPhone(step)
			}
		})
	})
	if err := al.SetSoftVolume(p, false); err != nil {
		slog.Warn("bluetooth: leaving a phone's audio unscaled", "err", err)
	}
	level := levelFor(media.Get().Volume())
	if err := al.SetLevel(p, level); err != nil {
		slog.Warn("bluetooth: handing a phone the volume", "err", err)
	}
	f.mu.Lock()
	f.levels[string(p.Path)] = level
	f.ours[string(p.Path)] = time.Now().Add(echoFor)
	f.mu.Unlock()
}

// echoing is whether a level read from the stream at path at now may be the phone saying back the
// level handed to it from here, rounded: within echoFor of handing it. Called with mu held.
func (f *Feature) echoing(path string, now time.Time) bool { return now.Before(f.ours[path]) }

// echoFor is how long after a level is handed to a phone a level read from it may be that one said
// back, rounded.
const echoFor = 2 * time.Second

// othersHadIt is whether something other than a phone has the speaker: a remote such as Music
// Assistant, or a receiver of the device's own (AirPlay, Spotify Connect, DLNA), named by what it is
// receiving. Then no phone is owed the speaker afterward.
func othersHadIt(receiving string, external bool) bool {
	return external || (receiving != "" && !strings.HasPrefix(receiving, media.BluetoothPrefix))
}

// speakerFree is whether a phone another took the speaker from may take it back: nothing of the
// device's own loaded, playing or paused, and no remote such as Music Assistant playing to it.
func speakerFree(playing, paused, external bool) bool { return !playing && !paused && !external }

// holdForJoin is tone with the connect tone held back when a phone has just joined phone pairing in
// the same look: the pairing has its own chime (onPaired).
func holdForJoin(tone []speaker.Note, joined string) []speaker.Note {
	if joined != "" && slices.Equal(tone, speaker.ToneBTConnect) {
		return nil
	}
	return tone
}

// handLevel passes the device's new volume step to the goroutine that tells the phone, replacing one
// still waiting: only the latest matters, and one goroutine keeps them in order. Never blocks.
func (f *Feature) handLevel(step int) {
	for {
		select {
		case f.levelTo <- step:
			return
		default:
		}
		select {
		case <-f.levelTo:
		default:
		}
	}
}

// sliderMove is whether a received stream's level, read now, is the phone's slider moved: not when
// nothing was known before or nothing changed, nor when the last level was handed to the phone from
// here and this is the phone saying it back rounded to its own steps (within one of the device's).
func sliderMove(level, last int, known, ours bool) bool {
	if !known || level == last {
		return false
	}
	if ours {
		d := stepFor(level) - stepFor(last)
		return d > 1 || d < -1
	}
	return true
}

// phoneSlider is a look at the stream playing here: its level other than last seen or set is the
// phone's slider moved, and sets the device's volume. A raise stops where night volume says
// (media.CapRaise); the slider is then moved back to where the device ended up (tellPhone, by way of
// OnLevel).
func (f *Feature) phoneSlider(p bluealsa.PCM) {
	level := p.Level()
	f.mu.Lock()
	last, known := f.levels[string(p.Path)]
	ours := f.echoing(string(p.Path), time.Now())
	f.levels[string(p.Path)] = level
	f.mu.Unlock()
	if !sliderMove(level, last, known, ours) {
		return
	}
	m := media.Get()
	from, to := m.Volume(), stepFor(level)
	if to > from {
		to = m.CapRaise(to)
	}
	if to != from {
		slog.Info("bluetooth: a phone's volume slider sets the volume", "level", level, "step", to)
		m.Set(to)
	}
}

// tellPhone moves the slider of the phone playing here to the device's volume step, unless it is at
// that step already: a slider being dragged is left alone, and the level it was dragged to is not
// handed back to it.
func (f *Feature) tellPhone(step int) {
	f.mu.Lock()
	al := f.alsa
	f.mu.Unlock()
	cur := media.Get().Receiving()
	if al == nil || cur == "" {
		return
	}
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return
	}
	names := map[string]string{}
	for _, d := range a.Devices() {
		names[string(d.Path)] = media.BluetoothPrefix + displayName(d)
	}
	for _, p := range al.PCMs() {
		if !p.Receivable() || !p.Running || names[string(p.Device)] != cur || stepFor(p.Level()) == step {
			continue
		}
		level := levelFor(step)
		f.mu.Lock()
		f.levels[string(p.Path)] = level
		f.ours[string(p.Path)] = time.Now().Add(echoFor)
		f.mu.Unlock()
		if err := al.SetLevel(p, level); err != nil {
			slog.Warn("bluetooth: handing a phone the volume", "err", err)
		}
	}
}

// linkTones is the tone the last look's change of connected audio devices plays: rising for one that
// connected, falling for one that went, nil for none. Not on the first look, which has nothing to
// compare with; not for a connect that ends a pairing, which has its own chime; and not for a device
// this side is connecting (busy: a pick, Reconnect) or dropping and connecting again on its own
// (hushed: the remake attach does), which would chime for the device's own work. What somebody asked
// for - Disconnect, Forget, forgetting or dropping the phones, Reconnect phone - keeps its tone, which
// says it was done. Called with mu held.
func (f *Feature) linkTones(devices []bluez.Device, speakerAddr string) []speaker.Note {
	now := map[string]bool{}
	busy := map[string]bool{}
	for _, d := range devices {
		addr := strings.ToLower(d.Address)
		if f.busy[addr] || time.Now().Before(f.hushed[addr]) {
			busy[string(d.Path)] = true
		}
		if d.Connected && (d.AudioSink || isPhone(d, speakerAddr)) {
			now[string(d.Path)] = true
		}
	}
	was, seen := f.linked, f.linkSeen
	f.linked, f.linkSeen = now, true
	if !seen {
		return nil
	}
	tone := linkTone(was, now, busy)
	if slices.Equal(tone, speaker.ToneBTConnect) && (f.state.Pairing || f.phone) {
		return nil // a pairing that worked has its own chime (onPaired) and its own words on the screen
	}
	return tone
}

// linkTone is the tone for the audio devices connected going from was to now: rising when one
// connected, else falling when one went, nil when nothing changed but devices in busy.
func linkTone(was, now, busy map[string]bool) []speaker.Note {
	for p := range now {
		if !was[p] && !busy[p] {
			return speaker.ToneBTConnect
		}
	}
	for p := range was {
		if !now[p] && !busy[p] {
			return speaker.ToneBTDisconnect
		}
	}
	return nil
}

// InCall is whether a call has the device, when a tone would be in the way. Set by the phone feature
// (phone.go's init), which knows: btaudio cannot import it, since what it imports comes back to
// btaudio (the diagnostics redact the paired devices' names).
var InCall = func() bool { return false }

func inCall() bool { return InCall() }

// received is a remote's stream that is running: what it sends, under the name it plays as.
type received struct {
	pcm  bluealsa.PCM
	dev  bluez.Device
	item string
}

// takeBack picks the displaced remote that takes the speaker back, if the speaker is free, and keeps
// displaced to the remotes still sending: one that stopped sending is no longer owed the speaker. The
// one picked is no longer displaced either, so a phone paused since is tried once, goes quiet and is
// let go. Called with mu held.
func takeBack(displaced map[string]bool, sending []received, free bool) *received {
	for item := range displaced {
		if !slices.ContainsFunc(sending, func(r received) bool { return r.item == item }) {
			delete(displaced, item)
		}
	}
	if !free {
		return nil
	}
	for i := range sending {
		if displaced[sending[i].item] {
			delete(displaced, sending[i].item)
			return &sending[i]
		}
	}
	return nil
}

// pairedIncoming is the agent telling us a remote paired with the device (rather than the device
// pairing out to a speaker it picked).
func (f *Feature) pairedIncoming(path string) {
	f.mu.Lock()
	// The agent is asked the same questions when this device pairs out to a speaker it picked or was
	// told to; only a pairing nobody here started is a remote joining.
	if f.state.Pairing && !f.picking && len(f.busy) == 0 {
		f.incoming = path
	}
	f.mu.Unlock()
	f.wake()
}

// pairingCode puts the code a pairing was confirmed with on the screen: the phone being paired shows
// the same six digits, so whoever is pairing can see it is their phone that joined, and not one
// nearby that asked first while pairing mode was on.
func (f *Feature) pairingCode(_, code string) {
	f.note("Pairing code " + code + ": the same as on your phone?")
	f.wake()
}

func displayName(d bluez.Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Address
}

// attach opens the stream and points the speaker at it.
//
// A stream the device set up itself — earbuds coming out of their case connect on their own —
// cannot be acquired from this side: bluetoothd answers NotAuthorized, and keeps answering. What
// works is a connection this side made, so after a couple of refusals the link is dropped and
// made again, once per connection.
func (f *Feature) attach(al *bluealsa.Client, p bluealsa.PCM, d bluez.Device) {
	s, err := al.Open(p)
	if err != nil {
		slog.Warn("bluetooth stream", "device", displayName(d), "err", err)
		f.mu.Lock()
		f.refusals++
		n := f.refusals
		f.mu.Unlock()
		if n == 2 {
			slog.Info("bluetooth: remaking the connection ourselves", "device", displayName(d))
			f.mu.Lock()
			// Dropped and made again here: neither half is a device going or coming (linkTones).
			f.hushed[strings.ToLower(d.Address)] = time.Now().Add(2*connectTimeout + 10*time.Second)
			f.mu.Unlock()
			safe.Go("bluetooth remake", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*connectTimeout)
				defer cancel()
				f.mu.Lock()
				a := f.adapter
				f.mu.Unlock()
				if a == nil {
					return
				}
				if err := a.Disconnect(ctx, d.Address); err != nil {
					slog.Warn("bluetooth disconnect", "device", displayName(d), "err", err)
				}
				time.Sleep(2 * time.Second)
				f.connect(ctx, d.Address, false)
			})
		}
		return
	}
	f.mu.Lock()
	f.refusals = 0
	f.mu.Unlock()
	f.mu.Lock()
	old := f.stream
	f.stream = s
	f.mu.Unlock()
	if old != nil {
		speaker.Get().SetSink(nil, "", 0, 0) // closes it
	}
	speaker.Get().SetSink(s, displayName(d), p.Rate, p.Channels)
	slog.Info("playing through bluetooth", "device", displayName(d), "codec", p.Codec, "rate", p.Rate, "channels", p.Channels)
	f.note(displayName(d) + " connected")
}

// detach puts the speaker back on the codec.
func (f *Feature) detach() {
	f.mu.Lock()
	s := f.stream
	f.stream = nil
	f.refusals = 0
	f.mu.Unlock()
	if s == nil {
		return
	}
	speaker.Get().SetSink(nil, "", 0, 0) // the speaker closes the stream
	slog.Info("bluetooth audio ended")
	f.note("Disconnected")
}

func (f *Feature) note(s string) {
	f.mu.Lock()
	f.state.Status = s
	f.mu.Unlock()
}

// SetPairing turns pairing mode on or off: discoverable, pairable and scanning while on, and off
// again on its own after a while.
func (f *Feature) SetPairing(on bool) {
	f.modes.Lock()
	defer f.modes.Unlock()
	f.setPairing(on)
}

// setPairing is SetPairing, with modes held.
func (f *Feature) setPairing(on bool) {
	f.mu.Lock()
	a := f.adapter
	if f.pairOff != nil {
		f.pairOff.Stop()
		f.pairOff = nil
	}
	f.state.Pairing = on && a != nil
	f.pairingSince, f.tried, f.incoming = time.Now(), map[string]bool{}, ""
	endPhone := on && f.phone
	if endPhone {
		f.phone, f.state.Phone = false, false
		f.phoneGen++
		if f.phoneOff != nil {
			f.phoneOff.Stop()
			f.phoneOff = nil
		}
	}
	phone := f.phone
	if on && a != nil {
		f.pairOff = time.AfterFunc(pairingFor, func() { f.SetPairing(false) })
		f.state.Status = "Put your earbuds in pairing mode"
	} else if strings.HasPrefix(f.state.Status, "Pairing code") {
		f.state.Status = "" // a question about a pairing that is over, which nothing else would replace
	}
	f.mu.Unlock()
	f.pairing.Set(on && a != nil)
	if endPhone {
		f.showPhone() // on still if a phone is connected; through showPhone, so it never flicks
	}
	if a == nil {
		slog.Warn("bluetooth pairing asked for, no adapter")
		return
	}
	if on || !phone { // phone pairing still wants the adapter findable
		if err := a.Pairing(on); err != nil {
			slog.Warn("bluetooth pairing mode", "on", on, "err", err)
		}
	}
	if err := a.Discover(on); err != nil {
		slog.Warn("bluetooth discovery", "on", on, "err", err)
	}
	slog.Info("bluetooth pairing mode", "on", on)
	f.wake()
}

// SetPhonePairing turns phone pairing on or off: findable, pairable and connectable, with no scan
// and no pick, so a phone (a new one or one already paired) can connect. Pairing mode, if on, ends
// first: it would connect the strongest speaker it hears.
func (f *Feature) SetPhonePairing(on bool) {
	f.modes.Lock()
	defer f.modes.Unlock()
	f.setPhonePairing(on, -1)
}

// setPhonePairing is SetPhonePairing, with modes held. A gen of 0 or more is the window a phoneOff
// was set for: if another has been opened or closed since, it does nothing.
func (f *Feature) setPhonePairing(on bool, gen int) {
	f.mu.Lock()
	stale := gen >= 0 && gen != f.phoneGen
	pairing := f.state.Pairing
	f.mu.Unlock()
	if stale {
		return
	}
	if on && pairing {
		f.setPairing(false)
	}
	f.mu.Lock()
	a := f.adapter
	if f.phoneOff != nil {
		f.phoneOff.Stop()
		f.phoneOff = nil
	}
	f.phoneGen++
	f.phone = on && a != nil
	f.state.Phone = f.phone
	if f.phone {
		gen := f.phoneGen
		f.phoneOff = time.AfterFunc(phoneFor, func() {
			f.modes.Lock()
			defer f.modes.Unlock()
			f.setPhonePairing(false, gen)
		})
	} else if strings.HasPrefix(f.state.Status, "Pairing code") && !f.state.Pairing {
		f.state.Status = "" // as when pairing mode ends
	}
	pairing = f.state.Pairing
	f.mu.Unlock()
	f.showPhone()
	if a == nil {
		slog.Warn("bluetooth phone pairing asked for, no adapter")
		return
	}
	if !on && pairing {
		return // pairing mode still wants the adapter findable
	}
	if err := a.Pairing(on); err != nil {
		slog.Warn("bluetooth phone pairing", "on", on, "err", err)
	}
	slog.Info("bluetooth phone pairing", "on", on)
	f.wake()
}

// showPhone publishes the phone switch: on while phone pairing is on or a phone is connected.
func (f *Feature) showPhone() {
	f.phoneShow.Lock()
	defer f.phoneShow.Unlock()
	f.mu.Lock()
	on := f.phone || (f.phoneLinked && !f.hangingUp)
	f.mu.Unlock()
	if f.phonePair.Get() != on {
		f.phonePair.Set(on)
	}
}

// disconnectPhones drops the connected phones; they stay paired and trusted, so each can connect
// again, from its own Bluetooth settings or called back with Reconnect phone. The switch shows off
// until the look that finds them gone (refresh), or at once if one would not go.
func (f *Feature) disconnectPhones() {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	failed := a == nil
	if a != nil {
		speaker := config.Get().Bluetooth.Audio
		for _, d := range a.Devices() {
			if !d.Connected || !isPhone(d, speaker) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			err := a.Disconnect(ctx, d.Address)
			cancel()
			if err != nil {
				failed = true
				slog.Warn("bluetooth phone disconnect", "device", displayName(d), "err", err)
			} else {
				slog.Info("bluetooth phone disconnected", "device", displayName(d))
			}
		}
	}
	if failed {
		f.mu.Lock()
		f.hangingUp = false
		f.mu.Unlock()
	}
	f.wake()
}

// ReconnectPhone calls the paired, trusted phones one after the other until one answers: a phone
// that left and came back does not look for the device by itself. Nothing is called while a phone
// is connected.
func (f *Feature) ReconnectPhone() {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return
	}
	devices := a.Devices()
	speaker := config.Get().Bluetooth.Audio
	for _, d := range devices {
		if d.Connected && isPhone(d, speaker) {
			return
		}
	}
	for _, d := range devices {
		if !d.Trusted || !isPhone(d, speaker) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		err := a.Connect(ctx, d.Address)
		cancel()
		if err == nil {
			slog.Info("bluetooth phone reconnected", "device", displayName(d))
			f.wake()
			return
		}
		slog.Info("bluetooth phone did not answer", "device", displayName(d), "err", err)
	}
	f.note("No phone answered")
	f.wake()
}

// ForgetPhones ends phone pairing and forgets every paired device that is not a speaker, which also
// drops it: whoever paired during phone pairing has to pair again in a window opened on purpose.
// The speakers and earbuds the device plays to are kept.
func (f *Feature) ForgetPhones() {
	f.mu.Lock()
	f.hangingUp = true
	f.mu.Unlock()
	f.SetPhonePairing(false)
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	failed := a == nil
	if a != nil {
		speaker := config.Get().Bluetooth.Audio
		for _, d := range a.Devices() {
			if !isPhone(d, speaker) {
				continue
			}
			if err := a.Remove(d.Address); err != nil {
				failed = true
				slog.Warn("bluetooth forget", "device", displayName(d), "err", err)
			} else {
				slog.Info("bluetooth phone forgotten", "device", displayName(d))
			}
		}
	}
	if failed {
		f.mu.Lock()
		f.hangingUp = false
		f.mu.Unlock()
	}
	f.wake()
}

// phoneLook is what lookAtPhones finds: the phone that joined (its address, or ""), whether any phone
// is connected and the first one's name, and the phones whose audio is up, by D-Bus path.
type phoneLook struct {
	joined string
	linked bool
	name   string
	audio  map[string]bool
}

// lookAtPhones looks at the connected phones. While phone pairing is open, the first whose audio has
// come up since the last look (was) has joined; a phone whose audio was already up has not, so one
// connected before the window opened does not end it.
func lookAtPhones(devices []bluez.Device, speaker string, audioUp, was map[string]bool, open bool) phoneLook {
	l := phoneLook{audio: map[string]bool{}}
	for _, d := range devices {
		if !d.Connected || !isPhone(d, speaker) {
			continue
		}
		if !l.linked {
			l.linked, l.name = true, displayName(d)
		}
		path := string(d.Path)
		l.audio[path] = audioUp[path]
		if open && l.joined == "" && audioUp[path] && !was[path] {
			l.joined = d.Address
		}
	}
	return l
}

// isPhone is whether a paired device is a phone, or anything else that plays to the device, rather
// than earbuds or a speaker it plays to: it sends audio (A2DP source) or says it is a phone, and is
// neither the remembered speaker nor one pairing mode lists for its icon without an A2DP sink.
func isPhone(d bluez.Device, speaker string) bool {
	if !d.Paired || d.AudioSink || strings.EqualFold(d.Address, speaker) {
		return false
	}
	switch d.Icon {
	case "audio-headset", "audio-headphones", "audio-card":
		return false
	case "phone":
		return true
	}
	return slices.ContainsFunc(d.UUIDs, func(u string) bool { return strings.EqualFold(u, bluez.AudioSource) })
}

// PhonePairing reports whether phone pairing is on.
func (f *Feature) PhonePairing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phone
}

// acceptsPairing is what the pairing agent asks: a pairing coming in is answered yes in pairing
// mode and in phone pairing, and refused the rest of the time.
func (f *Feature) acceptsPairing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state.Pairing || f.phone
}

// Pairing reports whether pairing mode is on.
func (f *Feature) Pairing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state.Pairing
}

// Choose is a tap on a listed device: pair it if it is not, then connect. Runs in the background.
func (f *Feature) Choose(address string) {
	safe.Go("bluetooth choose", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*connectTimeout)
		defer cancel()
		f.connect(ctx, address, true)
	})
}

func (f *Feature) setBusy(address string, on bool) {
	f.mu.Lock()
	if on {
		f.busy[strings.ToLower(address)] = true
	} else {
		delete(f.busy, strings.ToLower(address))
	}
	f.mu.Unlock()
	f.wake()
}

// connect pairs (when asked) and connects one device, remembers it, and ends pairing mode on
// success.
func (f *Feature) connect(ctx context.Context, address string, pair bool) {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return
	}
	d, ok := a.Device(address)
	if !ok {
		slog.Warn("bluetooth connect: unknown device", "address", address)
		return
	}
	name := displayName(d)
	f.setBusy(address, true)
	defer f.setBusy(address, false)

	if pair && !d.Paired {
		f.note("Pairing " + name + "…")
		if err := a.Pair(ctx, address); err != nil {
			slog.Warn("bluetooth pair", "device", name, "err", err)
			f.note("Pairing " + name + " failed")
			return
		}
		if err := a.Trust(address, true); err != nil {
			slog.Warn("bluetooth trust", "device", name, "err", err)
		}
	}
	f.note("Connecting " + name + "…")
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := a.Connect(cctx, address); err != nil {
		if strings.Contains(err.Error(), "Already Connected") {
			err = nil
		} else {
			slog.Warn("bluetooth connect", "device", name, "err", err)
			f.note("Could not connect " + name)
			return
		}
	}
	if err := config.Set().Bluetooth().Audio(address, name); err != nil {
		slog.Error("saving the bluetooth device failed", "err", err)
	}
	f.mu.Lock()
	f.state.Remembered = name
	f.mu.Unlock()
	slog.Info("bluetooth connected", "device", name)
	if pair {
		f.SetPairing(false)
		onPaired()
	}
	f.wake()
}

// Reconnect tries the remembered device now.
func (f *Feature) Reconnect() {
	addr := f.remembered()
	if addr == "" {
		slog.Info("bluetooth reconnect: no device to reach for")
		f.note("No device remembered")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	f.connect(ctx, addr, false)
}

// Disconnect drops the connected audio device; it stays paired and trusted.
func (f *Feature) Disconnect() {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	if a == nil {
		return
	}
	for _, d := range a.Devices() {
		if d.Connected && d.AudioSink {
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			err := a.Disconnect(ctx, d.Address)
			cancel()
			if err != nil {
				slog.Warn("bluetooth disconnect", "device", displayName(d), "err", err)
			}
		}
	}
	f.wake()
}

// Forget removes the remembered device's bond.
func (f *Feature) Forget() {
	f.mu.Lock()
	a := f.adapter
	var playing dbus.ObjectPath
	if f.stream != nil {
		playing = f.stream.Device
	}
	f.mu.Unlock()
	remembered := config.Get().Bluetooth.Audio
	var devices []bluez.Device
	if a != nil {
		devices = a.Devices()
	}
	addr := forgetTarget(devices, playing, remembered)
	if a != nil && addr != "" {
		if err := a.Remove(addr); err != nil {
			slog.Warn("bluetooth forget", "err", err)
		}
	}
	if strings.EqualFold(addr, remembered) {
		_ = config.Set().Bluetooth().Audio("", "")
		f.mu.Lock()
		f.state.Remembered = ""
		f.mu.Unlock()
	}
	f.wake()
}

// PairedNames is the names of the devices paired with this one and of the remembered one: earbuds,
// speakers and phones, which are often named after whoever owns them (the diagnostics leave them out).
func (f *Feature) PairedNames() []string {
	f.mu.Lock()
	a := f.adapter
	f.mu.Unlock()
	var devices []bluez.Device
	if a != nil {
		devices = a.Devices()
	}
	return pairedNames(devices, config.Get().Bluetooth.AudioName)
}

// pairedNames is the names of the paired devices among devices, and remembered's.
func pairedNames(devices []bluez.Device, remembered string) []string {
	var out []string
	if remembered != "" {
		out = append(out, remembered)
	}
	for _, d := range devices {
		if d.Paired && d.Name != "" && !slices.Contains(out, d.Name) {
			out = append(out, d.Name)
		}
	}
	return out
}

// forgetTarget is the device Forget forgets: the one the device is playing to when it plays to one,
// which is the one the screen's row names (Connected), else the remembered one.
func forgetTarget(devices []bluez.Device, playing dbus.ObjectPath, remembered string) string {
	if playing != "" {
		for _, d := range devices {
			if d.Path == playing {
				return d.Address
			}
		}
	}
	return remembered
}
