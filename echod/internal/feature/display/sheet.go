//go:build !dot

package display

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/netbird"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remote"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/streaming"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tailscale"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The settings screen's contents: each category's rows from the scene, the lists its choices open,
// and what a tap on any of it does. The renderer records where each control landed as it draws, so
// a tap is matched to the frame on the screen rather than to geometry worked out a second time.

// categoryCard is the card for the open category.
func categoryCard(sv sheetView) cardView {
	st := sv.st
	var v cardView
	if dv, ok := deviceCard(sv); ok {
		v = dv
		v.scroll = st.cardScroll
		return withLocks(v)
	}
	switch {
	case st.cat == catAlarms:
		v = alarmsCard(sv)
	default:
		rows, note := categoryRows(sv)
		v = cardView{title: tr(categoryTitleKeys[st.cat]), blurb: tr(categoryBlurbKeys[st.cat]), rows: rows, note: note}
	}
	for i := range v.rows {
		if builtinOptionRow(v.rows[i].id) {
			v.rows[i].value = optionLabel(v.rows[i].value)
		}
	}
	v.rows = adaptRows(v.rows, sv)
	v.scroll = st.cardScroll
	return withLocks(v)
}

// withLocks marks the card's rows that are behind the settings lock while it is on, for the padlock
// beside their names.
func withLocks(v cardView) cardView {
	if !security.Locked() {
		return v
	}
	rows := make([]settingRow, len(v.rows))
	for i, row := range v.rows {
		row.locked = rowLocked(row.id)
		rows[i] = row
	}
	v.rows = rows
	return v
}

// categoryRows is the open category's rows, and a note to show when it has none.
func categoryRows(sv sheetView) (rows []settingRow, note string) {
	st := sv.st
	switch st.cat {
	case catDisplay:
		rows := []settingRow{
			{id: "brightness", label: tr("brightness"), kind: ctlStepper, value: fmt.Sprintf("%d%%", st.brightness)},
			{id: "auto", label: tr("auto_brightness"), sub: tr("follows_the_room_s_light"), kind: ctlToggle, on: st.auto},
		}
		if hasDimmest && st.auto {
			rows = append(rows, settingRow{id: "dimmest", label: tr("dimmest"), sub: tr("how_dark_auto_brightness_goes_in_a_dark_room"),
				kind: ctlStepper, value: fmt.Sprintf("%d%%", dimmestSetting())})
		}
		// Beside the light's own rows: it is about who else may put the screen out (screen_room.go). The
		// label is Screen ignores room light commands cut to what fits on the Spot, and the line under it
		// says only what the setting does, which is true whether or not Home Assistant already exposed
		// the light to Assist (that one is unticked in Home Assistant, docs/setup.md).
		rows = append(rows, settingRow{id: "roomlights", label: tr("ignore_room_lights"),
			sub: tr("out_of_area_targets_in_home_assistant"), kind: ctlToggle, on: roomLightsIgnored()})
		rows = append(rows, settingRow{id: "night", label: tr("night_hours"), kind: ctlChoice, value: nightRowValue(st.night)})
		if nightCanHappen(st.night) {
			rows = append(rows, settingRow{id: "atnight", label: tr("at_night"), sub: atNightSubText(),
				kind: ctlChoice, value: atNightOptions[atNightIndex()]})
			if nightClockOn() {
				rows = append(rows, settingRow{id: "nightstyle", label: tr("night_clock"), sub: tr("how_the_night_clock_looks"),
					kind: ctlChoice, value: nightStyleLabel()})
				if hasGlowRow {
					rows = append(rows, settingRow{id: "nightglow", label: tr("night_clock_brightness"), sub: tr("shown_as_you_change_it_at_night"),
						kind: ctlStepper, value: fmt.Sprintf(tr("level_of"), glowLevelSet(), len(glowSteps))})
				}
			}
			if hasNightTap && config.Get().Screen.NightLight {
				rows = append(rows, settingRow{id: "nighttap", label: tr("tap_lifts_the_night_light"),
					sub: tr("a_tap_brings_the_screen_back_until_the_room_is_quiet"), kind: ctlToggle, on: config.Get().Screen.NightTapLifts})
			}
		}
		// The light first, then each group under its name: what is changed most near the top.
		rows = append(rows, settingRow{label: tr("look"), kind: ctlHeading})
		if hasSkins {
			rows = append(rows, settingRow{id: "skin", label: tr("skin"), sub: tr("a_whole_look_over_the_theme_and_font"), kind: ctlChoice, value: skins[skinIndex()].label})
		}
		rows = append(rows, themeRows()...)
		rows = append(rows,
			fontRow(),
			clockStyleRow(),
			settingRow{id: "clock", label: tr("clock_format"), kind: ctlChoice, value: clockOptions[clockIndex()]},
		)
		rows = append(rows, clockLayoutRows()...)
		rows = append(rows,
			settingRow{label: tr("home_screen"), kind: ctlHeading},
			settingRow{id: "slideshow", label: tr("slideshow"), sub: tr("photos_from_home_assistant"), kind: ctlChoice, value: slideshowLabels()[slideshowIndex()]},
		)
		if slideshowIndex() != 0 {
			rows = append(rows, slideshowRows(st.demo)...)
		}
		rows = append(rows,
			settingRow{id: "musicstrip", label: tr("now_playing"), sub: tr("full_page_or_a_strip_over_the_clock"), kind: ctlChoice, value: stripOptionText()},
			settingRow{id: "follow", label: tr("now_playing_follows"), sub: tr("another_speaker_s_music_while_this_one_is_quiet"), kind: ctlChoice, value: followText(st.demo)},
			settingRow{id: "lyrics", label: tr("lyrics"), sub: tr("the_words_in_time_looked_up_at_lrclib"), kind: ctlToggle, on: home.LyricsOn()},
		)
		// Calls, Assist and the turn screen need a microphone: a screen without one leaves them out.
		if hasMicrophone() {
			rows = append(rows, settingRow{id: "callbutton", label: tr("call_button"), sub: tr("on_the_home_screen_devices_and_contacts"), kind: ctlToggle, on: callButton.Load()})
		}
		if hasClockTap {
			sub := tr("ui_start_assist_open_the_dashboard_or_the_deck_or_nothing")
			if !hasMicrophone() {
				sub = tr("ui_open_the_dashboard_or_the_deck_or_nothing")
			}
			rows = append(rows, settingRow{id: "clocktap", label: tr("tap_on_the_clock"), sub: sub, kind: ctlChoice, value: clockTapChoices()[clockTapIndex()].label})
		}
		rows = append(rows,
			settingRow{label: tr("weather"), kind: ctlHeading},
			settingRow{id: "weatherfx", label: tr("weather_animation"), sub: tr("rain_snow_and_storms_move_on_the_forecast"), kind: ctlToggle, on: weatherAnimation.Load()},
			settingRow{id: "alerts", label: tr("weather_alerts"), sub: tr("the_nws_s_alerts_for_home_in_the_u_s"), kind: ctlToggle, on: home.AlertsOn()},
			settingRow{id: "radarsrc", label: tr("radar_source"), sub: tr("automatic_uses_the_nws_in_the_lower_48"), kind: ctlChoice, value: home.RadarSourceOptions()[home.RadarSourceIndex()]},
		)
		// How the calendar looks, on a screen with a calendar page and once it shows a calendar.
		rows = append(rows, calendarLookRows()...)
		rows = append(rows,
			settingRow{label: tr("pop_ups"), kind: ctlHeading},
			settingRow{id: "camtime", label: tr("camera_time"), sub: tr("how_long_a_camera_opened_here_stays_up"), kind: ctlChoice, value: cameraTimes[cameraTimeIndex()].label},
		)
		if hasCameraLook {
			rows = append(rows,
				settingRow{id: "camfit", label: tr("ui_camera_picture"), sub: tr("ui_whole_filling_the_screen_or_the_camera_s_own_size"), kind: ctlChoice, value: cameraFits[cameraFitIndex()].label},
				settingRow{id: "cambands", label: tr("ui_camera_strips"), sub: tr("ui_under_its_name_and_at_its_foot_solid_or_fading_into_it"), kind: ctlChoice, value: cameraBandStyles[cameraBandsIndex()].label},
				settingRow{id: "camshade", label: tr("ui_camera_strips_shade"), sub: tr("ui_how_dark_those_strips_are"), kind: ctlChoice, value: cameraShades[cameraShadeIndex()].label},
			)
		}
		rows = append(rows,
			settingRow{id: "answertime", label: tr("answer_time"), sub: tr("how_long_an_answer_stays_up_a_tap_clears_it"), kind: ctlChoice, value: answerTimes[answerTimeIndex()].label},
		)
		if hasTimerStyles {
			rows = append(rows, settingRow{id: "timerstyle", label: tr("timers"), sub: tr("how_running_timers_show_on_the_clock"), kind: ctlChoice, value: timerStyles[timerStyleIndex()].label})
		}
		if hasEqualizer && hasMicrophone() {
			rows = append(rows, settingRow{id: "turnstyle", label: tr("turn_screen"), sub: tr("classic_or_a_picture_that_moves_with_the_voice"), kind: ctlChoice, value: turnStyles[turnStyleIndex()].label})
		}
		return withPresence(rows), ""
	case catSound:
		mic := tr("ui_listening")
		if st.muted {
			mic = tr("ui_muted")
		}
		// The speaker first, then each group under its name: what is changed most near the top.
		rows := []settingRow{
			{id: "volume", label: tr("volume"), kind: ctlStepper, value: fmt.Sprintf(tr("level_of"), st.volume, sheetVolumeSteps)},
			{id: "bass", label: tr("bass"), sub: toneSub(), kind: ctlStepper, value: toneValue(config.Get().Speaker.Bass)},
			{id: "treble", label: tr("treble"), kind: ctlStepper, value: toneValue(config.Get().Speaker.Treble)},
		}
		if speaker.HasJack {
			rows = append(rows, settingRow{id: "output", label: tr("audio_output"), sub: tr("where_the_sound_goes_with_headphones_in"),
				kind: ctlChoice, value: media.Get().Output()})
		}
		if media.Get().SpeakerGainAvailable() {
			rows = append(rows, settingRow{id: "speakergain", label: tr("speaker_gain"),
				sub:  tr("0_db_is_normal_boosts_can_distort_especially_with_more_bass"),
				kind: ctlStepper, value: gainText(media.Get().SpeakerGain())})
		}
		// Without a microphone there is no voice to set up and no intercom to turn away: only the
		// sounds for timers are left of the Voice group.
		if hasMicrophone() {
			rows = append(rows, []settingRow{
				{label: tr("voice"), kind: ctlHeading},
				{id: "mic", label: tr("microphone"), sub: tr("the_mute_button_does_this_too"), kind: ctlToggle, on: !st.muted, value: mic},
				{id: "wakeword", label: tr("wake_word"), kind: ctlChoice, value: st.wakeWord},
				{id: "wakesens", label: tr("wake_word_threshold"), sub: tr("higher_wakes_by_mistake_less_often"), kind: ctlStepper,
					value: fmt.Sprintf("%.2f", config.Get().Wake.Slot(0).Threshold)},
				{id: "waketone", label: tr("wake_sound"), kind: ctlChoice, value: config.Get().Wake.Slot(0).Tone.Label()},
				voiceRow(),
				{id: "hasounds", label: tr("home_assistant_sounds"), sub: tr("for_muting_and_timers"), kind: ctlToggle, on: !config.Get().Speaker.ClassicSounds},
			}...)
		} else {
			rows = append(rows,
				settingRow{label: tr("sounds"), kind: ctlHeading},
				settingRow{id: "hasounds", label: tr("home_assistant_sounds"), sub: tr("for_timers"), kind: ctlToggle, on: !config.Get().Speaker.ClassicSounds})
		}
		rows = append(rows, []settingRow{
			{label: tr("quiet"), kind: ctlHeading},
			{id: "quiet", label: tr("quiet_hours"), sub: quietSub(), kind: ctlChoice, value: quietValue()},
			{id: "sleep", label: tr("sleep_timer"), sub: sleepSub(), kind: ctlChoice, value: sleepValue()},
		}...)
		if hasMicrophone() {
			rows = append(rows, settingRow{id: "dnd", label: tr("do_not_disturb"), sub: tr("intercom_calls_from_other_rooms_are_turned_away"), kind: ctlToggle, on: config.Get().Home.DoNotDisturb})
		}
		rows = append(rows, []settingRow{
			{label: tr("music"), kind: ctlHeading},
			{id: "sendspin", label: tr("music_assistant_player"), sub: tr("play_music_in_sync_with_other_rooms"), kind: ctlToggle, on: st.sendspin},
		}...)
		return append(withStreaming(rows),
			settingRow{label: tr("cameras"), kind: ctlHeading},
			settingRow{id: "camerasound", label: tr("camera_sound"), sub: tr("a_camera_s_own_audio_while_its_view_is_up"), kind: ctlToggle, on: home.CameraSound()},
		), ""
	case catConnections:
		return connectionRows(sv), ""
	case catSecurity:
		return securityRows(sv), ""
	case catGeneral:
		return generalRows(sv), ""
	}
	return nil, ""
}

// withStreaming adds AirPlay and Spotify Connect to the Sound card's rows, where the device has them
// (feature/streaming), and DLNA, which every device has (feature/dlna).
func withStreaming(rows []settingRow) []settingRow {
	c := config.Get().Streaming
	if streaming.Here {
		rows = append(rows,
			settingRow{id: "airplay", label: "AirPlay", sub: tr("play_to_it_from_an_iphone_ipad_or_mac"), kind: ctlToggle, on: c.AirPlay})
		if streaming.Has2() {
			rows = append(rows,
				settingRow{id: "airplay2", label: "AirPlay 2", sub: tr("ui_group_it_with_other_airplay_2_speakers"), kind: ctlToggle, on: !c.AirPlayClassic})
		}
		rows = append(rows,
			settingRow{id: "spotify", label: "Spotify Connect", sub: tr("play_to_it_from_the_spotify_app_premium"), kind: ctlToggle, on: c.Spotify})
	}
	return append(rows,
		settingRow{id: "dlna", label: "DLNA", sub: tr("play_to_it_from_music_apps_and_servers"), kind: ctlToggle, on: c.DLNA})
}

// securityRows are the Privacy & Security card's: how the device can be reached, and how it reaches
// Home Assistant.
func securityRows(sv sheetView) []settingRow {
	sec, st := sv.security, sv.st
	var rows []settingRow
	if !sec.SSHAvailable {
		rows = append(rows, settingRow{label: "SSH", kind: ctlValue, value: tr("ui_not_managed_here")})
	} else {
		sub := tr("ui_closed")
		switch {
		case sec.SSH && len(sec.Keys) == 0:
			sub = tr("ui_on_but_no_key_yet_so_nothing_listens")
		case sec.SSH && sec.SSHRunning:
			sub = tr("ui_port_22_keys_only")
		case sec.SSH:
			sub = tr("ui_starting")
		case sec.SSHRunning:
			sub = tr("ui_stopping")
		}
		keys := tr("ui_none")
		if len(sec.Keys) > 0 {
			keys = strings.Join(sec.Keys, ", ")
		}
		rows = append(rows,
			settingRow{id: "ssh", label: "SSH", sub: sub, kind: ctlToggle, on: sec.SSH},
			settingRow{label: tr("ssh_keys"), sub: tr("sent_from_home_assistant"), kind: ctlValue, value: keys})
	}
	lockSub := tr("ui_a_pin_before_most_settings_change")
	if security.LockSet() {
		lockSub = tr("ui_on_turn_off_to_remove_the_pin")
	}
	rows = append(rows, settingRow{id: "settingslock", label: tr("settings_lock"), sub: lockSub, kind: ctlToggle, on: security.LockSet()})
	rows = append(rows, settingRow{id: "dropin", label: tr("allow_drop_in"), sub: tr("intercom_calls_connect_by_themselves_after_a_chime"),
		kind: ctlToggle, on: config.Get().Home.DropIn})
	remoteSub := tr("ui_scripts_signed_with_this_device_s_key")
	if !remote.HasKey() {
		remoteSub = tr("ui_make_a_key_on_the_setup_page_first")
	}
	rows = append(rows, settingRow{id: "remoteapi", label: tr("remote_api"), sub: remoteSub, kind: ctlToggle, on: config.Get().Remote.On})
	rows = append(rows, settingRow{id: "callring", label: tr("call_ring"), sub: tr("how_a_call_rings_here"),
		kind: ctlChoice, value: phone.RingSounds[phone.RingSoundIndex()]})
	link := settingRow{label: tr("home_assistant_link"), sub: tr("encrypted_with_this_device_s_key"), kind: ctlValue, value: tr("ui_encrypted")}
	if !sec.Encrypted {
		link.sub, link.value = tr("ui_add_the_device_in_home_assistant_to_encrypt_it"), tr("ui_not_encrypted")
	}
	certs := settingRow{label: tr("certificate_checks"), sub: tr("for_downloads_updates_always_check"), kind: ctlValue, value: tr("ui_on")}
	if st.insecureTLS {
		certs.sub, certs.value = tr("ui_turned_off_in_home_assistant"), "Off"
	}
	rows = append(rows, cameraTestRows()...)
	return append(rows,
		settingRow{id: "camweb", label: tr("camera_on_the_network"), sub: tr("no_login"), kind: ctlToggle, on: sec.Camera},
		settingRow{id: "screenweb", label: tr("screen_on_the_network"), sub: tr("no_login"), kind: ctlToggle, on: sec.Screen},
		settingRow{id: "talkback", label: tr("talk_through_cameras"), sub: tr("talk_on_the_camera_page"), kind: ctlToggle, on: sec.TalkBack},
		setupRow(st.demo),
		link, certs)
}

// generalRows are the General card's: the device's name, weather, updates, what it is, and Restart.
func generalRows(sv sheetView) []settingRow {
	st := sv.st
	fw := firmware.Get()
	updates := settingRow{id: "updates", label: tr("updates"), sub: fmt.Sprintf(tr("this_version"), st.version), kind: ctlChoice,
		value: capitalize(fw.Channel().Label()), button: tr("ui_check_now")}
	switch {
	case st.checking:
		updates.sub = tr("ui_checking")
	case fw.Offered() != "":
		updates.sub, updates.button = fmt.Sprintf(tr("update_ready"), fw.Offered(), st.version), tr("ui_install")
	}
	if !update.Published() {
		// A panel's app updates it from one place: no channel to choose, only the button.
		updates.kind, updates.value = ctlButton, ""
	}
	restart := settingRow{id: "restart", label: tr("restart"), sub: tr("back_in_about_a_minute"), kind: ctlDanger, button: tr("restart")}
	if !st.restartArm.IsZero() && st.now.Sub(st.restartArm) < restartWindow {
		restart.sub, restart.button = tr("ui_tap_again_to_restart_now"), "Confirm"
	}
	rows := []settingRow{{label: tr("name"), sub: tr("change_it_on_the_setup_page"), kind: ctlValue, value: st.name}}
	rows = append(rows, calendarRows()...)
	return append(rows,
		settingRow{id: "weather", label: tr("weather"), sub: tr("shown_with_the_clock"), kind: ctlChoice, value: st.weather, button: tr("ui_show")},
		settingRow{id: "timezone", label: tr("time_zone"), sub: zoneSub(), kind: ctlChoice, value: zoneValue()},
		settingRow{id: "screenlang", label: tr("screen_language"), sub: tr("its_dates_its_weather_and_what_it_listens_for"),
			kind: ctlChoice, value: langOptions[langIndex()]},
		updates,
		settingRow{label: tr("about"), kind: ctlValue, value: deviceModel + " · slot " + st.slot},
		restart,
	)
}

// forgetRow is Forget for the earbuds or speaker the device plays to, under its row: it asks twice,
// the second tap within restartWindow of the first.
func forgetRow(sv sheetView, name string) settingRow {
	row := settingRow{id: "btforget", label: fmt.Sprintf(tr("forget_device"), name), sub: tr("asks_twice"), kind: ctlDanger, button: tr("ui_forget")}
	if !sv.st.forgetArm.IsZero() && sv.now.Sub(sv.st.forgetArm) < restartWindow {
		row.sub, row.button = tr("ui_tap_again_to_forget_it"), "Confirm"
	}
	return row
}

// connectionRows are the Connections card's: Wi-Fi, Bluetooth audio, Secure Connections, and the
// Bluetooth proxy.
func connectionRows(sv sheetView) []settingRow {
	st, bt := sv.st, sv.bt
	wifiRow := settingRow{label: tr("wi_fi"), sub: st.address, kind: ctlValue, value: st.wifiName}
	if st.wifiOK {
		wifiRow.id, wifiRow.kind, wifiRow.button = "wifi", ctlButton, "Change"
	}
	if st.address == "-" {
		wifiRow.sub = tr("ui_no_address_yet")
	}
	rows := []settingRow{wifiRow}
	if st.addressSet != "" {
		rows = append(rows, settingRow{id: "address", label: tr("network_address"), sub: st.addressSet, kind: ctlButton, button: tr("ui_change")})
	}
	switch {
	case !bt.Available:
		rows = append(rows, settingRow{label: tr("bluetooth_audio"), sub: tr("not_available_on_this_build"), kind: ctlValue})
	case bt.Connected != "":
		rows = append(rows, settingRow{id: "bt", label: bt.Connected, sub: cmpOr(bt.Status, tr("ui_bluetooth_audio_connected")), kind: ctlButton, button: tr("ui_disconnect")})
	case bt.Remembered != "":
		// What it is doing, while it does something; otherwise plainly not connected.
		row := settingRow{id: "bt", label: bt.Remembered, sub: tr("bluetooth_audio"), kind: ctlButton, value: tr("not_connected"), button: tr("connect")}
		if bt.Status != "" {
			row.sub, row.value = bt.Status, ""
		}
		rows = append(rows, row)
	default:
		rows = append(rows, settingRow{label: tr("bluetooth_audio"), sub: cmpOr(bt.Status, tr("ui_earbuds_or_a_speaker")), kind: ctlValue, value: tr("ui_none_yet")})
	}
	if name := cmpOr(bt.Connected, bt.Remembered); bt.Available && name != "" {
		rows = append(rows, forgetRow(sv, name))
	}
	if bt.Available {
		rows = append(rows, settingRow{id: "pair", label: tr("pair_a_new_device"), sub: tr("put_it_in_pairing_mode_first"), kind: ctlButton, button: tr("ui_pair")})
	}
	// Off for a phone that pairs and is dropped a second or two later (#183). Changing it powers the
	// controller off and on, so what is connected drops for a moment, and the row says so.
	if bt.Available && !st.btSCFixed {
		rows = append(rows, settingRow{id: "btsc", label: tr("bluetooth_secure_connections"),
			sub: tr("off_if_a_phone_pairs_then_drops_drops_devices_briefly"), kind: ctlToggle, on: !st.btSCOff})
	}
	rows = append(rows, settingRow{id: "btproxy", label: tr("bluetooth_proxy"), sub: tr("lets_home_assistant_hear_nearby_devices"),
		kind: ctlToggle, on: st.btProxy})
	// Tailscale: its row says where it stands; the key goes in on the setup page.
	if !tailscale.Available() {
		return rows
	}
	ts := tailscale.Get().State()
	rows = append(rows, settingRow{id: "tailscale", label: "Tailscale", sub: tailscaleSub(ts), kind: ctlToggle, on: ts.On})
	// NetBird likewise, once it has been joined: until then there is nothing to turn on here.
	if nb := netbird.Get().State(); netbird.Available() && (nb.On || config.Get().Netbird.Joined) {
		rows = append(rows, settingRow{id: "netbird", label: "NetBird", sub: netbirdSub(nb), kind: ctlToggle, on: nb.On})
	}
	return rows
}

// netbirdSub is the NetBird row's line.
func netbirdSub(s netbird.Status) string {
	switch {
	case !s.On:
		return tr("ui_reach_this_device_from_away_off")
	case s.Downloading:
		return fmt.Sprintf(tr("netbird_downloading"), int(s.Got*100))
	case s.State == "Connected" && s.Name != "":
		return fmt.Sprintf(tr("connected_as"), s.Name)
	case s.State == "NeedsLogin" || s.State == "LoginFailed" || s.State == "SessionExpired":
		return tr("ui_needs_a_setup_key_the_setup_page_connections")
	case s.Problem != "":
		return s.Problem
	}
	return tr("ui_starting_variant")
}

// tailscaleSub is the Tailscale row's line: where the device stands on the tailnet.
func tailscaleSub(s tailscale.Status) string {
	switch {
	case !s.On:
		return tr("ui_reach_this_device_from_away_off")
	case s.State == "Running" && s.Name != "":
		return fmt.Sprintf(tr("connected_as"), s.Name)
	case s.State == "NeedsLogin":
		return tr("ui_needs_a_key_the_setup_page_connections")
	case s.Problem != "":
		return s.Problem
	}
	return tr("ui_starting_variant")
}

// catByName is a category from its rail name or card title in any case, for /screen.png?sheet= and
// Home Assistant; the old tab names still work.
func catByName(name string) (category, bool) {
	for c := category(0); c < categories; c++ {
		if strings.EqualFold(categoryNames[c], name) || strings.EqualFold(categoryTitles[c], name) {
			return c, true
		}
	}
	switch strings.ToLower(name) {
	case "device", "theme":
		return catDisplay, true
	case "bluetooth", "wifi", "wi-fi":
		return catConnections, true
	case "security":
		return catSecurity, true
	}
	return 0, false
}

var (
	clockOptions     = []string{clock12Label, clock24Label}
	slideshowOptions = []string{"Off", "Background", "Screensaver"}
	slideshowModes   = []string{"", config.SlideshowBackground, config.SlideshowScreensaver}
)

func clockIndex() int {
	if clock24.Load() {
		return 1
	}
	return 0
}

func slideshowIndex() int {
	mode := home.Get().SlideshowMode()
	for i, m := range slideshowModes {
		if m == mode {
			return i
		}
	}
	return 0
}

// nightText is a night window as the clock would say it: "10 PM – 6 AM", "7 PM – 9:30 AM", or "Never".
// nightByHARow is the night row's choice that leaves the night to Home Assistant's Night mode switch.
const nightByHARow = "Controlled by Home Assistant"

// nightRowLabel is what the Display card calls the night setting.
const nightRowLabel = "Night hours"

// nightCanHappen is whether the night can come with the hours v: they make one, or Home Assistant has
// the night (Night hours set to Controlled by Home Assistant), whose Night mode switch turns it on
// whatever the hours are. The At night choice matters only then.
func nightCanHappen(v string) bool {
	if config.Get().Screen.NightByHA {
		return true
	}
	_, _, ok := nightWindow(v)
	return ok
}

// gainText is the speaker gain as the Sound card shows it: "0 dB", "+3 dB".
func gainText(db int) string {
	if db == 0 {
		return "0 dB"
	}
	return fmt.Sprintf("%+d dB", db)
}

// nightRowValue is what the night row says: the hours, or that Home Assistant has the night.
func nightRowValue(v string) string {
	if config.Get().Screen.NightByHA {
		return nightByHARow
	}
	return nightText(v)
}

func nightText(v string) string {
	from, to, ok := nightWindow(v)
	if !ok {
		return tr("ui_never")
	}
	return minuteText(from) + " – " + minuteText(to)
}

// minuteText is minutes since midnight as the clock would say it: "10 PM" on the hour, "9:30 AM" off it.
func minuteText(m int) string {
	if m%60 == 0 {
		return hourText(m / 60)
	}
	return clockTime(m/60, m%60)
}

// nightCustomRow is the night list's last choice: any start and end, picked hour then minutes.
const nightCustomRow = "Custom…"

// nightDraft holds the custom night's start while its end is being picked.
var nightDraft struct {
	sync.Mutex
	fromHour, from, toHour int
}

// nightMinutes are the minutes a custom night can start or end on.
var nightMinutes = []int{0, 15, 30, 45}

func hourText(h int) string {
	t := time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC)
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3 PM")
}

// pickerFor is the list of choices a row opens.
func pickerForRaw(id string, sv sheetView) (pickerView, bool) {
	if region, ok := strings.CutPrefix(id, "tzzone:"); ok {
		return zonePicker(region), true
	}
	switch id {
	case "alarmsound":
		p := pickerView{title: tr("alarm_sound"), opts: speaker.AlarmSounds(), cur: -1}
		for i, o := range p.opts {
			if o == alarm.Get().Sound() {
				p.cur = i
			}
		}
		return p, true
	case "alarmwords":
		says := config.AlarmSays()
		p := pickerView{title: tr("said_as_it_rings"), opts: config.Labels(says), cur: slices.Index(says, alarm.Get().Say())}
		return p, true
	case "alarmrise":
		rises := config.AlarmRises()
		return pickerView{title: tr("ui_gets_louder"), opts: config.Labels(rises), cur: slices.Index(rises, alarm.Get().Rise())}, true
	case "e.repeat":
		p := pickerView{title: tr("repeat"), opts: repeatLabels(), cur: -1}
		if sv.draft != nil {
			for i, days := range repeats {
				if days == sv.draft.alarm.Days {
					p.cur = i
				}
			}
		}
		return p, true
	case "e.sunrise":
		labels, _ := alarmSunrise()
		p := pickerView{title: tr("wake_with_light"), opts: labels, cur: 0}
		if sv.draft != nil {
			p.cur = alarmSunriseIndex(sv.draft.alarm)
		}
		return p, true
	case "atnight":
		return pickerView{title: tr("at_night"), opts: atNightOptions, cur: atNightIndex()}, true
	case "nightstyle":
		return nightStylePicker()
	case "night":
		p := pickerView{title: tr("night_hours"), cur: -1}
		cur := sv.st.night
		for i, n := range nightPresets {
			p.opts = append(p.opts, nightText(n))
			if n == cur {
				p.cur = i
			}
		}
		p.opts = append(p.opts, tr("night_custom"))
		if _, _, ok := nightWindow(cur); ok && p.cur < 0 {
			p.cur = len(p.opts) - 1
		}
		p.opts = append(p.opts, tr("night_by_ha"))
		if config.Get().Screen.NightByHA {
			p.cur = len(p.opts) - 1
		}
		return p, true
	case "nightfromh", "nighttoh":
		title := tr("ui_night_starts")
		if id == "nighttoh" {
			title = tr("ui_night_ends")
		}
		p := pickerView{title: title, cur: -1}
		for h := 0; h < 24; h++ {
			p.opts = append(p.opts, hourText(h))
		}
		return p, true
	case "nightfromm", "nighttom":
		nightDraft.Lock()
		h, title := nightDraft.fromHour, tr("ui_night_starts_at")
		if id == "nighttom" {
			h, title = nightDraft.toHour, tr("ui_night_ends_at")
		}
		nightDraft.Unlock()
		p := pickerView{title: title, cur: -1}
		for _, m := range nightMinutes {
			p.opts = append(p.opts, clockTime(h, m))
		}
		return p, true
	case "wakeword":
		p := pickerView{title: tr("wake_word"), cur: -1}
		cur := config.Get().Wake.Slot(0).ID
		for i, m := range wake.Lib().Ours() {
			p.opts = append(p.opts, cmpOr(m.Phrase, strings.ReplaceAll(m.ID, "_", " ")))
			if m.ID == cur {
				p.cur = i
			}
		}
		return p, len(p.opts) > 0
	case "ttsvoice":
		return voicePicker()
	case "waketone":
		p := pickerView{title: tr("wake_sound"), opts: config.Labels(speaker.WakeTones()), cur: -1}
		cur := config.Get().Wake.Slot(0).Tone.Label()
		for i, o := range p.opts {
			if o == cur {
				p.cur = i
			}
		}
		return p, true
	case "weather":
		_, names, cur := home.Get().WeatherChoices()
		if sv.st.demo {
			// Weather entities are often named for the street they are on.
			n := 0
			for i := 2; i < len(names); i++ {
				if i == cur {
					names[i] = "Home"
					continue
				}
				n++
				names[i] = fmt.Sprintf(tr("weather_number"), n)
			}
		}
		return pickerView{title: tr("weather"), opts: names, cur: cur}, len(names) > 0
	case "updates":
		p := pickerView{title: tr("updates"), cur: -1}
		for i, c := range update.Channels() {
			p.opts = append(p.opts, capitalize(c.Label()))
			if c == firmware.Get().Channel() {
				p.cur = i
			}
		}
		return p, true
	case "folder":
		return folderPicker(sv.st.folder, sv.st.demo), true
	case "clock":
		return pickerView{title: tr("clock_format"), opts: clockOptions, cur: clockIndex()}, true
	case "clockstyle":
		return clockStylePicker()
	case "font":
		return fontPicker()
	case "skin":
		return pickerView{title: tr("skin"), opts: skinOptions(), cur: skinIndex()}, true
	case "clockpos", "weatherpos", "datecolor":
		return clockLayoutPicker(id)
	case "camtime":
		return pickerView{title: tr("camera_time"), opts: cameraTimeOptions(), cur: cameraTimeIndex()}, true
	case "camfit":
		return pickerView{title: tr("ui_camera_picture"), opts: cameraLookLabels(cameraFits), cur: cameraFitIndex()}, true
	case "cambands":
		return pickerView{title: tr("ui_camera_strips"), opts: cameraLookLabels(cameraBandStyles), cur: cameraBandsIndex()}, true
	case "camshade":
		return pickerView{title: tr("ui_camera_strips_shade"), opts: cameraLookLabels(cameraShades), cur: cameraShadeIndex()}, true
	case "callring":
		return pickerView{title: tr("call_ring"), opts: phone.RingSounds, cur: phone.RingSoundIndex()}, true
	case "answertime":
		return pickerView{title: tr("answer_time"), opts: answerTimeOptions(), cur: answerTimeIndex()}, true
	case "timerstyle":
		return pickerView{title: tr("timers"), opts: timerStyleOptions(), cur: timerStyleIndex()}, true
	case "turnstyle":
		return pickerView{title: tr("turn_screen"), opts: turnStyleOptions(), cur: turnStyleIndex()}, true
	case "clocktap":
		return pickerView{title: tr("tap_on_the_clock"), opts: clockTapOptions(), cur: clockTapIndex()}, true
	case "radarsrc":
		return pickerView{title: tr("radar_source"), opts: home.RadarSourceOptions(), cur: home.RadarSourceIndex()}, true
	case "calendars":
		return calendarsPicker(), true
	case "calpopwhen":
		return pickerView{title: tr("pop_up"), opts: popupLeadLabels, cur: popupLeadIndex()}, true
	case "calpopallday":
		return pickerView{title: tr("all_day_events"), opts: popupAllDayOpts, cur: popupAllDayIndex()}, true
	case "calpopcals":
		return popupCalendarsPicker(), true
	case "musicstrip":
		return pickerView{title: tr("now_playing"), opts: stripChoices(), cur: stripIndexShared()}, true
	case "awayoff":
		return pickerView{title: tr("screen_off_when_nobody_is_near"), opts: awayLabels(), cur: awayIndex()}, true
	case "follow":
		_, names, cur := home.Get().FollowChoices()
		if sv.st.demo {
			names = demoPlayers(names)
		}
		return pickerView{title: tr("now_playing_follows"), opts: names, cur: cur}, len(names) > 0
	case "screenlang":
		return pickerView{title: tr("screen_language"), opts: langOptions, cur: langIndex()}, true
	case "newtimer":
		return pickerView{title: tr("new_timer"), opts: timerLabels, cur: -1}, true
	case "sleep":
		return pickerView{title: tr("sleep_timer"), opts: sleepLabels(), cur: sleepIndex()}, true
	case "quiet":
		return pickerView{title: tr("quiet_hours"), opts: quietLabels(), cur: quietIndex()}, true
	case "output":
		p := pickerView{title: tr("audio_output"), opts: media.OutputChoices(), cur: -1}
		for i, o := range p.opts {
			if o == media.Get().Output() {
				p.cur = i
			}
		}
		return p, speaker.HasJack
	case "sunrise":
		return pickerView{title: tr("wake_with_light"), opts: sunriseLabels(), cur: sunriseIndex()}, true
	case "timezone":
		p := pickerView{title: tr("time_zone"), opts: append([]string{followHA, common}, timezone.Regions()...), cur: -1}
		if !timezone.Get().SetHere() {
			p.cur = 0
		}
		return p, len(p.opts) > 1
	case "slideshow":
		return pickerView{title: tr("slideshow"), opts: slideshowLabels(), cur: slideshowIndex()}, true
	case "photoevery":
		return pickerView{title: tr("time_per_photo"), opts: everyOptions, cur: everyIndex()}, true
	}
	return devicePicker(id, sv)
}

// choose puts the i'th choice of a row's list in force.
func (d *Display) choose(id string, i int) {
	if region, ok := strings.CutPrefix(id, "tzzone:"); ok {
		chooseZone(region, i)
		return
	}
	switch id {
	case "atnight":
		d.setAtNight(i)
	case "nightstyle":
		d.setNightStyle(i)
	case "night":
		if i < len(nightPresets) {
			if err := config.Set().Screen().Night(nightPresets[i]); err != nil {
				slog.Warn("saving the night setting failed", "err", err)
			}
			if err := config.Set().Screen().NightByHA(false); err != nil {
				slog.Warn("saving the night setting failed", "err", err)
			}
			d.nightHoursChanged()
		} else if i == len(nightPresets) {
			if err := config.Set().Screen().NightByHA(false); err != nil {
				slog.Warn("saving the night setting failed", "err", err)
			}
			d.openPicker("nightfromh")
		} else if i == len(nightPresets)+1 {
			d.nightLeftToHA()
		}
	case "nightfromh", "nighttoh":
		if i < 0 || i > 23 {
			return
		}
		nightDraft.Lock()
		next := "nightfromm"
		if id == "nighttoh" {
			nightDraft.toHour, next = i, "nighttom"
		} else {
			nightDraft.fromHour = i
		}
		nightDraft.Unlock()
		d.openPicker(next)
	case "nightfromm":
		if i < 0 || i >= len(nightMinutes) {
			return
		}
		nightDraft.Lock()
		nightDraft.from = nightDraft.fromHour*60 + nightMinutes[i]
		nightDraft.Unlock()
		d.openPicker("nighttoh")
	case "nighttom":
		if i < 0 || i >= len(nightMinutes) {
			return
		}
		nightDraft.Lock()
		from, to := nightDraft.from, nightDraft.toHour*60+nightMinutes[i]
		nightDraft.Unlock()
		if from == to {
			slog.Info("night hours: the start and the end are the same time; not changed")
			return
		}
		if err := config.Set().Screen().Night(config.FormatWindow(from, to)); err != nil {
			slog.Warn("saving the night setting failed", "err", err)
		}
		d.nightHoursChanged()
	case "musicstrip":
		d.setMusicStrip(i)
	case "clockstyle":
		d.setClockStyle(i)
	case "font":
		d.setFont(i)
	case "skin":
		setSkin(d.skinSel(), i)
	case "clockpos", "weatherpos", "datecolor":
		d.chooseClockLayout(id, i)
	case "clock":
		on := i == 1
		if err := config.Set().Screen().Clock24(on); err != nil {
			slog.Warn("saving the clock format failed", "err", err)
			return
		}
		setClock24(d.clock, on)
	case "camtime":
		setCameraTime(d.camTime, i)
	case "camfit":
		setCameraFit(i)
	case "cambands":
		setCameraBands(i)
	case "camshade":
		setCameraShade(i)
	case "answertime":
		setAnswerTime(d.answerTime, i)
	case "callring":
		if i >= 0 && i < len(phone.RingSounds) {
			go phone.Get().SetRingSound(phone.RingSounds[i])
		}
	case "turnstyle":
		setTurnStyle(d.turnStyleSel(), i)
	case "timerstyle":
		setTimerStyle(d.timerStyleSel(), i)
	case "clocktap":
		setClockTap(d.clockTapSel(), i)
	case "radarsrc":
		go home.Get().SetRadarSource(i)
	case "calendars":
		if toggleCalendar(i) {
			d.openPicker("calendars") // stays open, for ticking another
		}
	case "calpopwhen":
		if i >= 0 && i < len(popupLeads) {
			_ = config.Set().Calendar().PopupLead(popupLeads[i])
			d.popupSettingsChanged()
		}
	case "calpopallday":
		_ = config.Set().Calendar().PopupAllDayNever(i == 1)
		d.popupSettingsChanged()
	case "calpopcals":
		if choosePopupCalendar(i) {
			d.openPicker("calpopcals")
		}
	case "wakeword":
		if models := wake.Lib().Ours(); i < len(models) {
			id := models[i].ID
			safe.Go("wake word from the screen", func() { voice.Get().ChooseWakeWord(id) })
		}
	case "ttsvoice":
		chooseVoice(i)
	case "waketone":
		if tones := config.Labels(speaker.WakeTones()); i < len(tones) {
			wakeword.Get().SetTone(0, tones[i])
		}
	case "awayoff":
		if i >= 0 && i < len(awayChoices) {
			m := awayChoices[i]
			safe.Go("presence wait from the screen", func() { presence.Get().SetScreenOff(m) })
		}
	case "follow":
		if entities, _, _ := home.Get().FollowChoices(); i < len(entities) {
			e := entities[i]
			safe.Go("followed player from the screen", func() { home.Get().ChooseFollow(e) })
		}
	case "weather":
		if entities, _, _ := home.Get().WeatherChoices(); i < len(entities) {
			e := entities[i]
			safe.Go("weather source from the screen", func() { home.Get().ChooseWeather(e) })
		}
	case "updates":
		if chans := update.Channels(); i < len(chans) {
			firmware.Get().SetChannel(chans[i].Label())
			d.checkUpdates()
		}
	case "folder":
		d.folderChoice(i)
	case "alarmsound":
		if names := speaker.AlarmSounds(); i < len(names) {
			alarm.Get().SetSound(names[i], true)
		}
	case "alarmwords":
		if says := config.AlarmSays(); i >= 0 && i < len(says) {
			alarm.Get().SetSay(says[i])
		}
	case "alarmrise":
		if rises := config.AlarmRises(); i >= 0 && i < len(rises) {
			alarm.Get().SetRise(rises[i])
		}
	case "e.repeat":
		if i < len(repeats) {
			d.editDraft(func(dr *alarmDraft) { dr.alarm.Days = repeats[i] })
		}
	case "e.sunrise":
		if _, values := alarmSunrise(); i < len(values) {
			d.editDraft(func(dr *alarmDraft) { dr.alarm.Sunrise = values[i] })
		}
	case "slideshow":
		if i < len(slideshowModes) {
			home.Get().ChooseSlideshowMode(slideshowModes[i])
		}
	case "photoevery":
		if i < len(everyDurations) {
			home.Get().SetSlideshowEvery(everyDurations[i])
		}
	case "newtimer":
		if i < len(timerLengths) {
			timer.Get().Start("Timer", timerLengths[i])
		}
	case "sleep":
		chooseSleep(i)
	case "quiet":
		chooseQuiet(i)
	case "output":
		if opts := media.OutputChoices(); speaker.HasJack && i < len(opts) {
			media.Get().SetOutput(opts[i])
		}
	case "sunrise":
		chooseSunrise(i)
	case "timezone":
		if i == 0 {
			if err := timezone.Get().Follow(); err != nil {
				slog.Warn("following Home Assistant's time zone failed", "err", err)
			}
			return
		}
		if i == 1 {
			d.openPicker("tzzone:" + common)
			return
		}
		if regions := timezone.Regions(); i-2 < len(regions) {
			d.openPicker("tzzone:" + regions[i-2])
		}
	case "screenlang":
		if i < len(langCodes) {
			if err := config.Set().Screen().Language(langCodes[i]); err != nil {
				slog.Warn("saving the screen language failed", "err", err)
			}
		}
	default:
		d.deviceChoose(id, i)
	}
}

// nextTap is a finger on the settings screen.
func (d *Display) nextTap(x, y int) {
	stillHere()
	z, ok := d.r.zoneAt(x, y)
	if !ok {
		return
	}
	switch z.kind {
	case zoneDone:
		d.closeSheet()
	case zoneCat:
		d.mu.Lock()
		d.cat, d.picker, d.restartArm, d.draft, d.colors = z.cat, "", time.Time{}, nil, false
		d.cardScroll, d.pickScroll = 0, 0
		d.mu.Unlock()
	case zoneAction:
		d.actionTap(z.id)
	case zoneDismiss:
		d.mu.Lock()
		d.picker = ""
		d.mu.Unlock()
	case zoneOption:
		d.mu.Lock()
		id := d.picker
		d.picker = ""
		d.mu.Unlock()
		d.tapOption(id, z.label, z.opt)
	case zoneRow:
		d.tapRow(z)
	}
}

// tapRow is a tap on a row, through the settings lock (gate, pin.go).
func (d *Display) tapRow(z zone) {
	if z.id == "weather" && z.part == partExtra {
		d.rowTap(z.id, z.part, z.opt) // the Show's forecast, which changes nothing
		return
	}
	gate(z.id, z.label, d.sheetUp, func() { d.rowTap(z.id, z.part, z.opt) })
}

// tapOption is a choice in an open list, through the settings lock. A list behind the lock is open
// while locked only when the lock came back with it up, or when it was opened from afar; the PIN then
// opens the lock and nothing more, since the list it was chosen from has gone and may read differently
// when it is opened again.
func (d *Display) tapOption(id, what string, opt int) {
	if rowLocked(id) {
		gate(id, what, d.sheetUp, nil)
		return
	}
	gate(id, what, d.sheetUp, func() { d.choose(id, opt) })
}

// rowTap is a tap on a row's control, past the settings lock: tapRow asks for the PIN first.
func (d *Display) rowTap(id string, p part, opt int) {
	if d.alarmRowTap(id, p, opt) {
		return
	}
	if d.deviceRowTap(id, p, opt) {
		return
	}
	switch id {
	case "brightness":
		switch p {
		case partMinus:
			d.stepBrightness(-25)
		case partPlus:
			d.stepBrightness(+25)
		}
	case "dimmest":
		switch p {
		case partMinus:
			d.stepDimmest(-1)
		case partPlus:
			d.stepDimmest(+1)
		}
	case "nightglow":
		switch p {
		case partMinus:
			d.stepGlow(-1)
		case partPlus:
			d.stepGlow(+1)
		}
	case "auto":
		d.mu.Lock()
		on := d.autoOn
		d.mu.Unlock()
		d.setAuto(!on, true)
	case "callbutton":
		setCallButtonSaved(d.callBtn, !callButton.Load())
	case "calpop":
		_ = config.Set().Calendar().Popups(!config.Get().Calendar.Popups)
		d.popupSettingsChanged()
	case "calpopsound":
		_ = config.Set().Calendar().PopupSilent(!config.Get().Calendar.PopupSilent)
		d.popupSettingsChanged()
	case "alerts":
		home.Get().SetAlertsOn(!home.AlertsOn())
	case "camerasound":
		home.Get().SetCameraSound(!home.CameraSound())
	case "lyrics":
		safe.Go("lyrics from the screen", func() { home.Get().SetLyricsOn(!home.LyricsOn()) })
	case "nighttap":
		d.setNightTapLifts(!config.Get().Screen.NightTapLifts)
	case "roomlights":
		d.setRoomLightsIgnored(!roomLightsIgnored())
	case "weatherfx":
		setWeatherAnimationSaved(d.weatherFx, !weatherAnimation.Load())
	case "dnd":
		go phone.Get().SetDoNotDisturb(!config.Get().Home.DoNotDisturb)
	case "hasounds":
		media.Get().SetHASounds(config.Get().Speaker.ClassicSounds)
	case "dropin":
		go phone.Get().SetDropIn(!config.Get().Home.DropIn)
	case "volume":
		switch p {
		case partMinus:
			media.Get().Adjust(-1)
		case partPlus:
			media.Get().Adjust(+1)
		}
	case "mic":
		mute.Get().Flip()
	case "speakergain":
		v := media.Get().SpeakerGain()
		switch p {
		case partMinus:
			v--
		case partPlus:
			v++
		default:
			return
		}
		if err := media.Get().SetSpeakerGain(max(0, min(v, config.SpeakerGainMax))); err != nil {
			slog.Error("changing speaker gain failed", "err", err)
		}
	case "wakesens":
		v := config.Get().Wake.Slot(0).Threshold
		switch p {
		case partMinus:
			v -= 0.02
		case partPlus:
			v += 0.02
		default:
			return
		}
		wakeword.Get().SetThreshold(0, math.Round(min(max(v, 0.5), 0.99)*100)/100)
	case "bass", "treble":
		c := config.Get().Speaker
		v := c.Bass
		if id == "treble" {
			v = c.Treble
		}
		switch p {
		case partMinus:
			v--
		case partPlus:
			v++
		default:
			return
		}
		v = min(max(v, -asp.ToneRange), asp.ToneRange)
		var err error
		if id == "treble" {
			err = config.Set().Speaker().Treble(v)
		} else {
			err = config.Set().Speaker().Bass(v)
		}
		if err != nil {
			slog.Error("saving a setting failed", "setting", id, "err", err)
			return
		}
		c = config.Get().Speaker
		speaker.Get().SetTone(asp.Tone{Bass: c.Bass, Treble: c.Treble})
	case "sendspin":
		sp := sendspin.Get()
		sp.SetEnabled(!sp.Enabled())
	case "ssh":
		security.Get().SetSSH(!config.Get().Security.SSH)
	case "tailscale":
		go func() { _ = tailscale.Get().SetOn(!config.Get().Tailscale.On) }()
	case "netbird":
		go func() { _ = netbird.Get().SetOn(!config.Get().Netbird.On) }()
	case "remoteapi":
		remote.Get().SetOn(!config.Get().Remote.On)
	case "camweb":
		security.Get().SetCamera(!config.Get().Security.Camera)
	case "screenweb":
		security.Get().SetScreen(!config.Get().Security.Screen)
	case "talkback":
		security.Get().SetTalkBack(!config.Get().Security.TalkBack)
	case "airplay":
		streaming.Get().SetAirPlay(!config.Get().Streaming.AirPlay)
	case "airplay2":
		streaming.Get().SetAirPlay2(config.Get().Streaming.AirPlayClassic)
	case "spotify":
		streaming.Get().SetSpotify(!config.Get().Streaming.Spotify)
	case "dlna":
		dlna.Get().Set(!config.Get().Streaming.DLNA)
	case "presence":
		safe.Go("presence from the screen", func() { presence.Get().SetOn(!config.Get().Presence.On) })
	case "settingslock":
		if security.LockSet() {
			if err := security.Get().SetPIN(""); err != nil {
				slog.Warn("clearing the settings lock failed", "err", err)
			}
		} else {
			openPINSet() // a new PIN, typed twice on the pad
		}
	case "sunface":
		if err := config.Set().Alarms().SunriseFace(!config.Get().Alarms.SunriseFace); err != nil {
			slog.Warn("saving the sun's face failed", "err", err)
		}
	case "setuppage":
		if setup.Get().On() {
			setup.Get().Close()
			return
		}
		setup.Get().Open()
	case "weather":
		if p == partExtra {
			d.showForecast()
			return
		}
		d.openPicker(id)
	case "updatecheck":
		d.rowTap("updates", partExtra, opt)
	case "updates":
		switch {
		case p != partExtra && update.Published():
			d.openPicker(id)
		case firmware.Get().Offered() != "":
			safe.Go("update install from the screen", func() { firmware.Get().Install(context.Background()) })
		default:
			d.checkUpdates()
		}
	case "restart":
		d.mu.Lock()
		armed := !d.restartArm.IsZero() && time.Since(d.restartArm) < restartWindow
		if !armed {
			d.restartArm = time.Now()
		}
		d.mu.Unlock()
		if armed {
			slog.Warn("restart asked for from the screen")
			restartNow()
		}
	case "bt":
		bt := btaudio.Get()
		switch st := bt.State(); {
		case st.Connected != "":
			bt.Disconnect()
		case st.Remembered != "":
			bt.Reconnect()
		}
	case "btforget":
		d.mu.Lock()
		armed := !d.forgetArm.IsZero() && time.Since(d.forgetArm) < restartWindow
		d.forgetArm = time.Time{}
		if !armed {
			d.forgetArm = time.Now()
		}
		d.mu.Unlock()
		if armed {
			safe.Go("bluetooth forget from the screen", btaudio.Get().Forget)
		}
	case "pair":
		d.closeSheet()
		btaudio.Get().SetPairing(true)
	case "btproxy":
		p := bluetooth.Get()
		safe.Go("bluetooth proxy from the screen", func() { p.SetEnabled(!p.Enabled()) })
	case "btsc":
		bt := btaudio.Get()
		safe.Go("bluetooth secure connections from the screen", func() { _ = bt.SetSecureConnections(!bt.SecureConnections()) })
	case "photofolder":
		d.openFolder(photosRoot, nil)
	case "shuffle":
		_, shuffle, _ := home.Get().SlideshowSettings()
		home.Get().SetSlideshowShuffle(!shuffle)
	case "weatherart":
		home.Get().SetSlideshowArt(!home.Get().SlideshowArt())
		d.wake()
	case "subfolders":
		_, _, subfolders := home.Get().SlideshowSettings()
		home.Get().SetSlideshowSubfolders(!subfolders)
	case "wholephoto":
		home.Get().SetSlideshowWholePhoto(!home.Get().SlideshowWholePhoto())
	case "photopairs":
		home.Get().SetSlideshowPairs(!home.Get().SlideshowPairs())
	case "photocaption":
		home.Get().SetSlideshowCaption(!home.Get().SlideshowCaption())
	case "immichfav":
		home.Get().SetImmichFavorites(!home.Get().ImmichFavorites())
	case "night", "atnight", "nightstyle", "clock", "clockstyle", "font", "skin", "clockpos", "weatherpos", "datecolor", "camtime", "camfit", "cambands", "camshade", "answertime", "callring", "turnstyle", "timerstyle", "radarsrc", "calendars", "calpopwhen", "calpopallday", "calpopcals", "musicstrip", "follow", "awayoff", "slideshow", "photoevery", "screenlang", "newtimer", "sleep", "sunrise",
		"timezone", "wakeword", "waketone", "ttsvoice", "quiet", "output", "clocktap":
		d.openPicker(id)
	}
}

// openPicker opens a row's list of choices, from its start.
func (d *Display) openPicker(id string) {
	d.mu.Lock()
	d.picker, d.pickScroll = id, 0
	d.mu.Unlock()
}

// OpenList opens the settings screen's list of choices for a row, by its id, for a look from afar
// (/screen.png?list=); it reports whether the row has one.
func (d *Display) OpenList(id string) bool {
	if id == "folder" {
		d.openFolder(photosRoot, nil)
		return true
	}
	if _, ok := pickerFor(id, sheetView{}); !ok {
		return false
	}
	d.openPicker(id)
	d.wake()
	return true
}

// sheetSwipe is a vertical swipe on the settings screen, a notch at a time: it scrolls an open list,
// or else the card, following the finger. The swipe that opened the screen is still reporting
// notches until its finger lifts; those are known by where they started and do nothing.
func (d *Display) sheetSwipe(g touch.Gesture) {
	if d.r == nil {
		return
	}
	stillHere()
	by := notchPx
	if g.Kind == touch.SwipeDown {
		by = -notchPx
	}
	cardMax, pickMax := d.r.scrollLimits()
	d.mu.Lock()
	defer d.mu.Unlock()
	if image.Pt(g.X, g.Y) == d.openedBy {
		return
	}
	if d.picker != "" {
		d.pickScroll = min(max(d.pickScroll+by, 0), pickMax)
		return
	}
	d.cardScroll = min(max(d.cardScroll+by, 0), cardMax)
}

// notchPx is how far a list scrolls for each notch a swipe reports: the touch screen's notch, so
// the list keeps up with the finger.
const notchPx = 40

// checkUpdates looks for an update, showing Checking… on the Updates row until the answer is in.
func (d *Display) checkUpdates() {
	d.mu.Lock()
	if d.checking {
		d.mu.Unlock()
		return
	}
	d.checking = true
	d.mu.Unlock()
	safe.Go("update check from the screen", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		firmware.Get().Check(ctx)
		d.mu.Lock()
		d.checking = false
		d.mu.Unlock()
		d.wake()
	})
}

// stepBrightness moves the brightness ceiling by pct, between a quarter and full.
func (d *Display) stepBrightness(by int) {
	pct := min(max(d.ceilingOrDefault()+by, 25), 100)
	d.apply(true, pct, true)
}
