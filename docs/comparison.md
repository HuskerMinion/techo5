![](comparison/banner.jpg)

# Comparison

People often ask how the projects that bring Echo devices and other screens to Home Assistant
compare. This page lists what each one covers. It isn't a ranking: each project is good work with
its own focus, and the right one depends on your devices and what you want from them.

**[Open the interactive version](https://huskerminion.github.io/techo5/comparison/)**: the header stays put while you scroll, you can filter
by section or project, and each cell shows its note and source. Or **[answer a few questions](https://huskerminion.github.io/techo5/comparison/choose/)**
about your devices and what you want, and see which projects fit.

This page lives in the TECHO5 repo and is kept by TECHO5's author, so read it with that in mind.
To keep it even, it was built the same way for every project, TECHO5 included:

1. A separate reviewer for each project read only that project's public repos (code, docs,
   releases and pull requests) and listed every feature it found, with a source link.
2. The rows come from all six lists: every feature area that two or more projects share has a
   row, and each project named four of its own central features as rows too.
3. Each project's reviewer answered every row for that project, using the row definitions at the
   bottom of this page, and a second reviewer checked every answer again.

Every cell shows only what each project's own GitHub repos say: its code, docs and releases, and
for devices also what users report in its issues and discussions (the note says when a device
rests on user reports). Reports elsewhere (Reddit, Discord, forums) aren't counted, and nothing
here was tested by me. A ❔ means nothing was found there, not that it can't work.

Checked October 23, 2026. All of these projects move fast, so some cells will be out of date
soon. If you spot something wrong or missing, please [open an issue](https://github.com/HuskerMinion/techo5/issues) and it will be fixed.
Projects are in alphabetical order.

## The projects

**echo-dot-assist**

- What it is: Replacement software on the device: the hassmic daemon replaces the Alexa client on rooted Echo speakers; firewall, setup scripts, HA blueprint ([source](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md))
- Started: 2026-09-21 ([source](https://github.com/Gamer92000/echo-dot-assist/commits/main))
- Releases: v2026.10.17.151309, 2026-10-17; 53 GitHub releases: 4 stable, 49 pre-releases ("beta" from main, latest v2026.10.21.213542-beta, 2026-10-21) ([source](https://github.com/Gamer92000/echo-dot-assist/releases))
- License: MIT; third-party parts keep own (BSD-2/CC0, MIT-0, CC0, BSD-3, LGPL-2.1+ freeaptx) ([source](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/LICENSE))
- Devices: Released: Echo Dot 3 (2018, donut), Echo Dot 2 (biscuit), Echo 2 (radar). In progress: Echo Show 5 1st gen, Show 8 1st and 2nd gen ([source](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/README.md))
- What runs on the device: Fire OS stays, with Amazon's audio front end and wake word engine; hassmic daemon replaces Alexa. Needs only Home Assistant ([source](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md))
- Install: Unlock and root over USB (Dot 3: wires on test pads; Echo 2: soldering); guided scripts/setup.sh on a bare-metal Linux PC (main/beta: setup.sh in Windows Git Bash for an already unlocked Echo) ([source](https://github.com/Gamer92000/echo-dot-assist/blob/a4fe0bdb6ac6914b1a02cda9f4a1a4f4ace94750/README.md#install))

**EchoLocal**

- What it is: Replacement software on the device (echod) plus host CLI installer (echolocal); optional HACS integration with dashboard and card ([source](https://github.com/ygelfand/echolocal/blob/0.0.9/README.md))
- Started: echolocal 2026-07-26; echolocal-hacs 2026-08-04; libcountertop 2026-10-06 (split out of echolocal) ([source](https://github.com/ygelfand/echolocal/commits/main))
- Releases: echolocal 0.0.11, 2026-10-21: 11 releases plus 5 pre-releases. echolocal-hacs v0.0.13, 2026-10-21: 13 releases. libcountertop: 1 pre-release (cast-engine-1, 2026-10-22, a cast engine binary) ([source](https://github.com/ygelfand/echolocal/releases))
- License: echolocal MIT; echolocal-hacs MIT; libcountertop (shared library) AGPL-3.0 ([source](https://github.com/ygelfand/echolocal/blob/0.0.9/LICENSE))
- Devices: Echo Dot 2nd gen (Fire OS 6), Echo Show 5 1st/2nd gen, Echo Show 8 1st/2nd gen. In progress: Echo 2 (draft PR) ([source](https://github.com/ygelfand/echolocal/blob/0.0.10/README.md#devices))
- What runs on the device: Fire OS stays, Amazon services disabled; echod (Go) plus small native helpers run as init services; no separate server needed ([source](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/parts/echolocal.rc))
- Install: Unlock/root and TWRP via XDA guides; USB to a PC with Android platform-tools (adb); run echolocal CLI (macOS, Linux, Windows); HACS optional ([source](https://github.com/ygelfand/echolocal/blob/0.0.11/docs/src/content/docs/start/before.mdx#L15-L18))

**EchoMuse**

- What it is: Replacement software on the Echo (Go firmware; own emOS userspace by default) plus a controller (HA add-on or Docker) with web dashboard; separate wake-word trainer. ([source](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md))
- Started: 2026-05-01 (single repo) ([source](https://github.com/wilbowes/EchoMuse/commits/main))
- Releases: Firmware v2.18.1, 2026-10-15 (controller 2.26.1); emOS emos-v0.10, 2026-10-02; Early Access v2.19.0-ea.1, 2026-10-19 (controller 2.27.0-ea.1) and emOS emos-v0.11-ea.1, 2026-10-23. 73 GitHub releases: 66 full, 7 pre-releases. ([source](https://github.com/wilbowes/EchoMuse/releases))
- License: MIT for all parts; third-party components keep own licenses; emOS ships busybox (GPL-2.0). ([source](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/LICENSE))
- Devices: Released: Echo Dot 2nd gen only (FireOS 5 or 6 unlock). In progress: Echo 2 (emOS Early Access pre-release emos-v0.11-ea.1), Echo Dot 3rd gen (fork, #527), Echo Show 8, Echo Show 5, Echo Spot gen 1 (#991). ([source](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md))
- What runs on the device: emOS (own init/busybox on Amazon kernel, no Android) or FireOS 5 with root; EchoMuse Go firmware. Separate controller server required. ([source](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/emos/README.md))
- Install: Unlock/root over USB with third-party firebreak (macOS/Linux/Windows PC); then controller add-on/Docker; browser WebUSB wizard (Chrome/Edge) installs. ([source](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/quickstart.md))

**Kiosk Satellite**

- What it is: Android app installed on a stock OS; optional plugin SDK; blueprint scripts for HA ([source](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md))
- Started: 2026-07-15 (app repo); 2026-09-10 (plugins repo) ([source](https://github.com/jxlarrea/kiosk-satellite/commits/main))
- Releases: 2026.10.38, 2026-10-23; 307 GitHub releases, 0 marked pre-release (63 early v0.x-beta tags) ([source](https://github.com/jxlarrea/kiosk-satellite/releases/tag/2026.10.38))
- License: App: CC BY-NC-ND 4.0 plus plugin exception; plugin SDK and plugins repo: Apache-2.0 ([source](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/LICENSE))
- Devices: Android 7.0+ phones, tablets, panels; guides: Fire tablets, Meta Portal, Raspberry Pi 4, Echo Dot 2nd gen (headless, Kiosk Satellite Edition image) ([source](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.30/docs/echo-dot.md))
- What runs on the device: Stock Android or Fire OS stays (Echo Dot: Kiosk Satellite Edition LineageOS); the app and its foreground service run; no separate server ([source](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.30/docs/echo-dot.md))
- Install: Sideload APK; optional adb grants or device-owner command from a PC; Echo Dot: unlock and flash the Kiosk Satellite Edition image, app installs on first boot ([source](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.30/docs/echo-dot.md))

**TECHO5**

- What it is: Replacement firmware: Alpine Linux image plus one Go daemon (echod) on Echo devices and the Skylight Calendar; the same daemon in an Android app (TECHO5 Panel) for tablets and the Portal; optional dashcast server and Deck PC agent ([source](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md))
- Started: techo5 2026-09-14; techo5-dot 2026-09-16; techo5-spot 2026-09-16; techo5-athena 2026-10-15; techo5-panel 2026-10-18 (public 2026-10-19) ([source](https://github.com/HuskerMinion/techo5/commits/main))
- Releases: v1.6.3 (2026-10-23), 126 releases incl. 3 pre-releases; Dot dot-v1.0.31 (92, 1 pre); Spot spot-v1.4.3 (100, 1 pre); Panel app v0.1.425910 (2026-10-23, 2) ([source](https://github.com/HuskerMinion/techo5/releases))
- License: MIT (techo5, techo5-dot, techo5-spot, techo5-checkers, techo5-panel); techo5-athena has no license file, its README says code MIT, kernel GPL-2.0, text and images free to share with credit ([source](https://github.com/HuskerMinion/techo5-athena/blob/cfe228f0c8cd177faae87fc2efcb6d69a803c7de/README.md#L182-L185))
- Devices: Echo Show 5 gen 1/2, Show 8 gen 1/2, Spot gen 1, Dot gen 2, Dot gen 3 2018 (one unit), Echo gen 2 (one unit), Skylight Calendar 15 (no microphone: screen and speaker only); with the TECHO5 Panel app: Fire HD 8 (10th gen), Galaxy Tab S6 Lite, Meta Portal (2nd gen); in progress: Fire HD 8 8th gen (Panel issue #1) ([source](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md))
- What runs on the device: Echos and Skylight: no Android/Fire OS, Alpine Linux with the echod daemon; tablets and Portal: Android app with echod inside; no separate server needed (dashcast optional for streamed dashboards) ([source](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md))
- Install: Unlock (amonet; saiyaku for Show 8 gen 2; kamakiri-donut kit for Dot gen 3); Show 5 and Show 8 gen 1 need LineageOS 18.1 drivers; USB; Python installer on Windows/Linux/macOS; Echo gen 2 and Dot gen 3 need soldering to pads; Skylight: no unlock, adb on stock firmware; tablets and Portal: no unlock, adb install of the Panel app ([source](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md))

**View Assist**

- What it is: HA integration plus dashboard, views and blueprints; optional VACA Android app with its own HA integration ([source](https://dinki.github.io/View-Assist/docs/intro))
- Started: View-Assist 2024-03-07; integration 2025-02-01; VACA app source 2025-07-06; VACA integration 2025-07-08 ([source](https://github.com/dinki/View-Assist/commits/main))
- Releases: Integration 2026.7.0 (2026-07-08), 19 + 2 pre-releases; VACA v0.13.5 (2026-10-10), 26 + 1 pre-release; app source repo 2; View-Assist none ([source](https://github.com/msp1974/ViewAssist_Companion_App/releases))
- License: View-Assist CC BY-NC 4.0; integration none stated; VACA integration and app Apache-2.0 ([source](https://github.com/dinki/View-Assist/blob/main/LICENSE.md))
- Devices: Android 8+ devices (VACA); docs: Echo Show 5/8 (LineageOS), Lenovo, NSPanel Pro, Xiaomi; ESPHome VPE, Atom Echo. In progress: Android 7, Portal+ ([source](https://github.com/msp1974/ViewAssist_Companion_App/wiki))
- What runs on the device: Stock Android kept; VACA app (or other browser/mic apps); needs Home Assistant with View Assist and VACA integrations ([source](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Getting-Started))
- Install: HACS for both integrations; sideload VACA APK; Lovelace cards via HACS; no unlock unless device needs custom OS ([source](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Getting-Started))

## Devices

✅ supported &nbsp; 🔧 after putting another OS on it first &nbsp; 🚧 in progress &nbsp; ❌ the project says it isn't &nbsp; ❔ not found &nbsp; † see the notes. Each symbol links to its source.

| Device | echo-dot-assist | EchoLocal | EchoMuse | Kiosk Satellite | TECHO5 | View Assist |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| Echo Dot 2nd gen | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/biscuit/README.md)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/README.md)† | [✅](https://github.com/wilbowes/EchoMuse/releases/tag/v2.18.1)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.31/docs/echo-dot.md)† | [✅](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L35-L38)† | ❔ |
| Echo Dot 3rd gen (2018) | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/donut/README.md)† | [❔](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/board/donut.go)† | [🚧](https://github.com/wilbowes/EchoMuse/issues/527)† | ❔ | [✅](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L246-L256)† | ❔ |
| Echo Dot 3rd gen (2019, 2020, with clock) | [❌](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#supported-echos)† | ❔ | [❌](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio)† | ❔ | [❌](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L251-L252)† | ❔ |
| Echo 2nd gen | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/radar/README.md)† | [🚧](https://github.com/ygelfand/echolocal/pull/64)† | [🚧](https://github.com/wilbowes/EchoMuse/releases/tag/emos-v0.11-ea.1)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/discussions/730#discussioncomment-19017072)† | [✅](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L187-L193)† | ❔ |
| Echo Show 5 1st gen | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/checkers/devices/checkers/README.md)† | [✅](https://github.com/ygelfand/echolocal/releases/tag/0.0.9) | [🚧](https://github.com/wilbowes/EchoMuse/pull/36)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/CHANGELOG.md#L156)† | [🔧](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#L21-L28)† | [🔧](https://dinki.github.io/View-Assist/docs/supported-devices/android-devices/amazon-echo-show5)† |
| Echo Show 5 2nd gen | ❔ | [✅](https://github.com/ygelfand/echolocal/releases/tag/0.0.9) | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio) | [🔧](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/.github/ISSUE_TEMPLATE/bug_report.yml#L79)† | [🔧](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#L18)† | [🔧](https://dinki.github.io/View-Assist/docs/supported-devices/android-devices/amazon-echo-show5)† |
| Echo Show 8 1st gen | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/d8042ed/devices/crown/README.md)† | [✅](https://github.com/ygelfand/echolocal/releases/tag/0.0.9) | [🚧](https://github.com/wilbowes/EchoMuse/pull/358)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/CHANGELOG.md#L40)† | [🔧](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#L22-L28)† | [🔧](https://dinki.github.io/View-Assist/docs/supported-devices/android-devices/amazon-echo-show8)† |
| Echo Show 8 2nd gen | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/1c00727d332c3b0ee4d13b59f702c78839431694/devices/athena/README.md)† | [✅](https://github.com/ygelfand/echolocal/releases/tag/0.0.10)† | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio) | [🔧](https://github.com/jxlarrea/kiosk-satellite/issues/236)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/install-athena.md#which-screen)† | ❔ |
| Echo Spot 1st gen | ❔ | [🚧](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/host/profile/rook.go)† | [❔](https://github.com/wilbowes/EchoMuse/issues/991)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/issues/952#issuecomment-6108639736)† | [🔧](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#L20)† | [🔧](https://github.com/msp1974/ViewAssist_Companion_App/issues/324)† |
| Fire tablets | ❔ | ❔ | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.31/docs/fire.md)† | [✅](https://github.com/HuskerMinion/techo5-panel#supported-devices)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki)† |
| Meta Portal | ❔ | ❔ | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.31/docs/portal.md)† | [✅](https://github.com/HuskerMinion/techo5-panel#supported-devices)† | [✅](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/device/DeviceUnsupportedFunctions.kt#L25-L30)† |
| Raspberry Pi | ❔ | ❔ | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/quickstart.md)† | [🔧](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.31/docs/raspberry-pi.md)† | [❔](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/deck.md#L74)† | [🔧](https://github.com/msp1974/ViewAssist_Companion_App/issues/97)† |
| Other Android devices | ❔ | ❔ | [❔](https://github.com/wilbowes/EchoMuse/blob/main/docs/faq.md#will-this-work-on-an-echo-dot-gen-3--show--studio) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.31/README.md#get-started)† | [✅](https://github.com/HuskerMinion/techo5-panel#supported-devices)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki)† |
| Other devices | [❔](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/README.md) | [❔](https://github.com/ygelfand/echolocal/blob/main/README.md#devices) | [❔](https://github.com/wilbowes/EchoMuse/blob/87a3d217456e89d09049b56240ef17fbb71329d4/device/pkg/board/board.go#L52) | ❔ | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/install-skylight.md)† | [✅](https://dinki.github.io/View-Assist/docs/supported-devices)† |

<details>
<summary>Notes to the devices</summary>

- **Echo Dot 2nd gen**: echo-dot-assist: biscuit; micro-USB, no soldering; EchoLocal: needs Fire OS 6 and TWRP; EchoMuse: needs one-time USB unlock (firebreak), runs FireOS or emOS; Kiosk Satellite: Needs Kiosk Satellite Edition LineageOS 14.1; headless, no screen; TECHO5: replaces Fire OS; installer starts from unlocked stock Fire OS
- **Echo Dot 3rd gen (2018)**: echo-dot-assist: donut; firmware 6574.1 only, wires on test pads; EchoLocal: only a placeholder 'donut' board, no install path; EchoMuse: community fork by @shortgame11​, issue #527; TECHO5: TECHO5 Dot release; tested on one unit; wires on test pads TM4, TM5, TM1
- **Echo Dot 3rd gen (2019, 2020, with clock)**: echo-dot-assist: crumpet/​doebrite: the Dot 3 unlock does not work; EchoMuse: FAQ: refreshed Gen 3 has no known unlock; TECHO5: README: the 2019 refresh and the model with a clock are other boards and not supported
- **Echo 2nd gen**: echo-dot-assist: radar; solder USB to the amplifier board; EchoLocal: draft PR #64 (radar); EchoMuse: radar on emOS in an Early Access pre-release (emos-v0.​11-ea.​1)​, run on one unit; not on FireOS; Kiosk Satellite: User report only, not in its docs: the Echo Dot 2nd gen guide worked on one Echo 2nd gen with a community LineageOS 14.1 build (discussion 730); TECHO5: TECHO5 Dot release; tested on one unit; USB inside case
- **Echo Show 5 1st gen**: echo-dot-assist: checkers branch; tried on one unit; EchoMuse: open PR #36 (checkers); Kiosk Satellite: Needs LineageOS (Android 11); no guide; project does not name the generation, users report 1st gen (issues 82, 214); TECHO5: LineageOS 18.1 installed first for drivers; tested on one unit; View Assist: needs LineageOS 18.1 (Android 11)
- **Echo Show 5 2nd gen**: Kiosk Satellite: Needs LineageOS (Android 11); no guide; named only as a bug-report example and in user reports (discussion 186, issue 601); TECHO5: LineageOS 18.1 (2026-09-04 build) installed first for its drivers; View Assist: needs LineageOS 18.1 (cronos)
- **Echo Show 8 1st gen**: echo-dot-assist: crown on checkers branch (PR #33, merged into checkers 2026-10-17); one unit, not on main; EchoMuse: draft PR #358 (crown), on LineageOS; Kiosk Satellite: Needs LineageOS (Android 11); no guide; project does not name the generation, users report 1st gen (issues 49, 416); TECHO5: LineageOS 18.1 installed first for drivers; tested on one unit; View Assist: needs LineageOS 18.1 (Android 11)
- **Echo Show 8 2nd gen**: echo-dot-assist: athena on checkers branch (PR #34, merged 2026-10-20); one unit, not on main; EchoLocal: athena, added in 0.0.10; Kiosk Satellite: User reports only, not in its docs: run on LineageOS 18.1 (Android 11) since 2026-08-18 (issues 236, 1079, 1115); TECHO5: own mainline kernel, no LineageOS; two screen panels tested, eight more known but untested; two units
- **Echo Spot 1st gen**: EchoLocal: early, undocumented​; installer wants LineageOS on Android 11; EchoMuse: board profile issue #991 filed for later, no code; Kiosk Satellite: User reports and a maintainer comment, not in its docs: run on LineageOS (issues 736, 952); TECHO5: LineageOS 18.1 installed first for drivers; then TECHO5 replaces it; View Assist: User reports only, not in its docs: VACA on LineageOS 18.1 (Android 11) (issues 104, 282, 324)
- **Fire tablets**: Kiosk Satellite: Fire OS 7/8; Fire OS 5 below Android 7 minimum; TECHO5: TECHO5 Panel app, no unlock; Fire HD 8 (10th gen) tested; Fire HD 8 8th gen being fixed (issue #1); View Assist: community reports: Fire 8 (2021), Fire 10 (2022)
- **Meta Portal**: Kiosk Satellite: Verified on Portal Go; others expected, untested; TECHO5: TECHO5 Panel app; Portal 2nd gen (10") tested; View Assist: user reports; Portal+ quirk in app; camera in open PR #39
- **Raspberry Pi**: EchoMuse: Pi-class box runs the controller only, not a speaker; Kiosk Satellite: Pi 4 on KonstaKANG LineageOS 23.2; needs /vendor mic edits; TECHO5: only companion tools (Deck agent, dashcast) run on a Pi; View Assist: Not in its docs; the VACA maintainer reports testing on a Pi with LineageOS (Android 15) (issue 97)
- **Other Android devices**: Kiosk Satellite: Android 7.0 or newer; TECHO5: TECHO5 Panel app; Galaxy Tab S6 Lite tested; other Android 8.0+ ARM tablets may work, untested; View Assist: Android 8+; Android 7 in open PR #58
- **Other devices**: TECHO5: Skylight Calendar 15 (150-CAL); no unlock; no microphone, so a screen and speaker only, no voice; View Assist: Lenovo ThinkSmart View/Smart Display, Xiaomi Clock, NSPanel Pro, ESPHome satellites

</details>

## The table

✅ yes &nbsp; 🟡 partly &nbsp; ❌ no (the project says so) &nbsp; ❔ not found &nbsp; 🚧 in progress (not in a release yet) &nbsp; — doesn't apply &nbsp; † see the notes after the table. Each symbol links to its source.

| **Basics** | echo-dot-assist | EchoLocal | EchoMuse | Kiosk Satellite | TECHO5 | View Assist |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| Replaces the device's OS | [❌](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md)† | [❌](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/android/services/packages.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [❌](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md)† | [❌](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Getting-Started)† |
| Works with other display or microphone apps | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#together-with-kiosk-satellite-tablets)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/tessera/tessera.go)† | ❔ | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/js-api.md#wake-word-handoff-protocol-voice-satellite)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/dashboards.md#tessera-screens-preview)† | [✅](https://dinki.github.io/View-Assist/docs/viewassist-setup/display-devices)† |
| Interface languages | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/CHANGELOG.md#2026-09-24)† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/say/messages/en.yaml)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/translations/en.yaml)† | [✅](https://github.com/jxlarrea/kiosk-satellite/tree/2026.10.29/app/l10n/effective)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#clock-and-home-screen)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/translations)† |
| Porting to new hardware | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/devices/README.md#porting-to-another-model)† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/board)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/boards.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/raspberry-pi.md)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/porting-plan.md)† | [🟡](https://dinki.github.io/View-Assist/docs/supported-devices)† |
| **Voice** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Home Assistant Assist satellite | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md) | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/satellite/satellite.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/assist_satellite.py)† |
| Follow-up listening | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/src/hassmic/proto_esphome.c)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/turn/conversation.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_esphome.py) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/lib/managers/voice/voice_session.dart) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Configuration-Options)† |
| HA can start a conversation | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/src/hassmic/proto_esphome.c)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/satellite/satellite.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#timers-announcements-and-conversations) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/voice/voice.go)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/assist_satellite.py) |
| On-device wake word | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#wake-word)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/README.md#every-device)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/listening.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#settings) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Wake-Word-Detection-&-Voice-Recognition)† |
| Custom wake word models | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#microwakeword)† | [✅](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.11/README.md#wake-words)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/custom-wake-words.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#a-wake-word-of-your-own)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Custom-Files) |
| Two wake words, two assistants | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#two-wake-words)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/wakeword/wakeword.go) | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#settings) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants) | [❔](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/satellite/SatelliteWakeWorkHandler.kt) |
| Wake word on a server | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#home-assistants)† | ❔ | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/listening.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#wake-word-on-home-assistant)† | [✅](https://dinki.github.io/View-Assist/docs/viewassist-setup/microphone-devices)† |
| Start a turn without the wake word | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#on-the-echo)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/satellite/satellite.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#home-assistant-entities-and-actions)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#7-voice)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/button.py)† |
| Cancel a request in progress | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#buttons-and-light-ring)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/display/screens/screens.go#L364-L369)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#home-assistant-entities-and-actions)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#7-voice)† | [🟡](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/satellite/Satellite.kt)† |
| Stop without the wake word | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#voice)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/inference/detect/stop.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#settings) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants) | [🟡](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/satellite/SatelliteWakeWorkHandler.kt)† |
| Nearest device answers | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#several-echos)† | ❔ | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#wake-word-arbitration)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#the-nearest-device-answers)† | ❔ |
| Echo cancellation | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/microphone.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#echo-cancellation)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#how-well-does-it-hear) | [🟡](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/audio/MicrophoneInput.kt)† |
| Noise suppression | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/hardware/mic/cancel.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#noise-suppression)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#how-well-does-it-hear) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Configuration-Options)† |
| Mic gain and leveling | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/microphone.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#microphone-gain) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#how-well-does-it-hear) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Configuration-Options) |
| Mic array mixing | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/hardware/mic/mixer.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#microphone-channel)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#how-well-does-it-hear)† | ❔ |
| External or USB microphone choice | ❔ | ❔ | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#microphone-channel) | ❔ | [✅](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/audio/AudioInRouter.kt)† |
| End of speech detected on device | [❌](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/src/hassmic/main.c)† | ❔† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#settings)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#whats-new)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/select.py)† |
| Speech right after the wake word | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#voice) | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/turn/conversation.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/CHANGELOG.md) | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#settings)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#sounds-of-your-own)† | [✅](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/audio/MicrophoneInput.kt)† |
| Replies start while being generated | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#voice) | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/wake.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/lib/managers/voice/voice_session.dart)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/assist_satellite.py)† |
| Saved voice recordings | ❔ | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/recordings/recordings.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#when-you-need-them)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#list-saved-voice-recordings) | [✅](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/satellite/AudioPipelineRecorder.kt)† |
| Voice-turn history | ❔ | [✅](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.11/src/tabs/activity.ts)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_db.py)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Diagnostics)† |
| Last-turn sensors in HA | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/a4fe0bdb6ac6914b1a02cda9f4a1a4f4ace94750/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/say/messages/en.yaml) | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_esphome.py)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#home-assistant-entities-and-actions)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#two-wake-words-two-assistants) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/sensor.py)† |
| Wake-word clips and near misses | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/a4fe0bdb6ac6914b1a02cda9f4a1a4f4ace94750/docs/GUIDE.md#in-home-assistant)† | [🚧](https://github.com/ygelfand/echolocal/pull/136)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/microphone.md#when-you-need-them)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#wake-word-clips)† | ❔ |
| Uses the stock audio front end | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md) | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/hardware/mic/mixer.go)† | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/689f3d5783bb7a79d3340c9444dc423b27dfc845/docs/echo-dot.md#wake-word)† | [🟡](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/docs/microphones.md#L30-L33)† | [❌](https://github.com/msp1974/ViewAssistCompanionApp/tree/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/wakeword)† |
| Whisper detection | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#whisper-detection)† | ❔ | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md) | ❔ | ❔ |
| Realtime speech-to-speech AI | ❔ | ❔ | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#realtime-conversations)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#realtime-voice)† | ❔ |
| Assistant without Home Assistant | ❔ | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.11/docs/src/content/docs/features/voice.mdx#L95-L98)† | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#realtime-conversations)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#voice-without-home-assistant)† | [❌](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Wake-Word-Detection-&-Voice-Recognition)† |
| Library of voice-command add-ons | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/blueprints/automation/drop_in.yaml)† | ❔ | ❔ | [🟡](https://github.com/jxlarrea/kiosk-satellite/tree/2026.10.38/blueprints/script)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/blueprints/README.md#voice)† | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences)† |
| Music by voice | ❔ | ❔ | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/docs/music-library.md#playing-music-by-voice)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/play-music-with-ma)† |
| **Audio and media** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| HA media player | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/media/player.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_player.py) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/dlna.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#under-the-hood) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/media_player.py)† |
| Music ducking | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#music) | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/media.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#voice-assistant-interplay) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#whats-new) | [✅](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/devices/entity_listeners.py) |
| Sendspin / Music Assistant player | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#music) | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/sendspin)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#sendspin-player) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | [🚧](https://github.com/msp1974/ViewAssist_Companion_App/commit/911de65c1e7ed01d70364145d0f90612007e34b6)† |
| Bluetooth speaker for phones | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#music)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/a2dp/a2dp.go)† | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/panel.md#L41-L42)† | ❔ |
| Play on a Bluetooth speaker | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#playing-on-a-bluetooth-speaker)† | ❔ | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/android/app/src/main/kotlin/me/jxl/kiosk_satellite/AudioRouting.kt)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/panel.md#L41-L42)† | ❔ |
| Headphone or line-out jack | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/CHANGELOG.md#2026-09-24)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/media/player.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/android/app/src/main/kotlin/me/jxl/kiosk_satellite/AudioRouting.kt)† | [🟡](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L195)† | ❔ |
| AirPlay receiver | ❔ | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.10/internal/feature/airplay/airplay.go)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#airplay)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | ❔ |
| DLNA renderer | ❔ | ❔ | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/dlna.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | ❔ |
| Google Cast receiver | ❔ | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/chromecast)† | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md) | [❌](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/video.md)† | ❔ |
| Separate volumes per stream | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.21.213542-beta/docs/GUIDE.md#separate-volumes)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/volume.go) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#configuration)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#7-voice)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/number.py)† |
| Equalizer | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/speaker.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | ❔ |
| Speaker gain boost | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page)† | ❔ | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md)† | ❔ |
| Own sounds and tones | ❔ | [✅](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.11/custom_components/echolocal/tones.py)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#sounds) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#sounds-of-your-own) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Custom-Files)† |
| Voice audio on another media player | ❔ | ❔ | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#play-sounds-on-a-media-player) | ❔ | [✅](https://dinki.github.io/View-Assist/docs/view-assist-configuration/satellite-creation)† |
| Internet radio | ❔ | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/radio.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#the-floating-player)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md) | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/play-radio-with-ma)† |
| Sleep and ambient sounds | ❔ | [✅](https://github.com/ygelfand/libcountertop/tree/b9aa28bf371a/pkg/audio/noise) | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/sound-machine)† |
| Sleep timer | ❔ | ❔ | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music) | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/sound-machine)† |
| Now playing screen | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/media/screen.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#full-screen-now-playing) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#screenshots) | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views/music)† |
| Show and control another player | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#following-another-player) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music) | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views/music)† |
| Synced lyrics | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md#lyrics) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#5-music)† | ❔ |
| Video playback on screen | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/chromecast/screen.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/dlna.md#what-plays)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/video.md)† | ❔ |
| **Screen** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Home Assistant dashboard on screen | [🚧](https://github.com/Gamer92000/echo-dot-assist/pull/58)† | [🟡](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/dashboard)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/dashboards.md)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Working-with-View-Assist) |
| Other web pages on screen | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#website) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/dashboards.md#web-pages)† | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/views/webpage)† |
| Settings on the device screen | —† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/settings) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/kiosk.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#screenshots) | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Settings-Menu)† |
| App drawer or launcher on screen | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/apps.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/kiosk.md#staying-in-front)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md)† | [✅](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration/dashboard-options)† |
| Return to home view | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/home.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#around-the-dashboard) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/dashboards.md#turning-it-on) | [✅](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration/default-options)† |
| Screen orientation | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screen.md)† | ❔ | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/select.py) |
| Keep screen on | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/say/messages/en.yaml)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screen.md) | ❔ | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py) |
| UI scale | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/screen.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/lib/managers/settings/definitions.dart) | ❔ | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/number.py)† |
| Clock faces | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/say/messages/en.yaml) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.36/docs/screensavers.md#clock)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#clock-and-home-screen)† | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views/clock) |
| Weather display | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/weather.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#weather) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#3-weather) | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views/weather) |
| Screensaver | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/idle.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#2-photos)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Controls,-Sensors-and-Events)† |
| Photo slideshow | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/photos/photos.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#the-modes) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#2-photos) | [🟡](https://dinki.github.io/View-Assist/docs/community-contributions/cc-views/slideshow)† |
| Immich photos | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/photos.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/immich.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#2-photos) | [🟡](https://dinki.github.io/View-Assist/docs/community-contributions/cc-tipsandtricks/immich_clock_background)† |
| Entity chips on the idle screen | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/at-a-glance.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#show-chips-along-the-foot-of-the-clock)† | [🟡](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration/dashboard-options)† |
| HA notifications on screen | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/display/message/message.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#notifications) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#show-a-notification) | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/device-alerts)† |
| Timers on screen | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/timer/timer.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/at-a-glance.md#voice-timers) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md) | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/views/alarm)† |
| Calendar on screen | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/calendar.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.33/docs/screensavers.md#calendar)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#calendar)† | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/view-calendar) |
| To-do and shopping lists | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/shopping.go) | —† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md)† | ❔ | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views/list)† |
| Answers shown as views | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/turn/showing.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#set-up)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md)† | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/views) |
| Display modes | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#configuration)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#night)† | [✅](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/const.py) |
| Weather radar and alerts | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/nws/nws.go)† | —† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md)† | ❔ |
| Night mode | —† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/night.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#clock)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#night) | [🟡](https://dinki.github.io/View-Assist/docs/developer-resources/general-information)† |
| Automatic brightness | [—](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#buttons-and-light-ring)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/brightness/auto.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screen.md#adaptive-brightness)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#clock-and-home-screen)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py)† |
| Themes | —† | [✅](https://github.com/ygelfand/libcountertop/blob/4683e2463a07/pkg/display/theme/custom.go)† | —† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/lib/managers/settings/definitions.dart)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#clock-and-home-screen)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py)† |
| Fonts | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/display/style/style.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#clock)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#clock-and-home-screen) | [✅](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration/dashboard-options)† |
| Screen on/off and brightness from HA | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/checkers/docs/GUIDE.md#buttons-and-light-ring)† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/screen.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#controls)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#the-screen-and-the-rooms-lights) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py) |
| **Cameras** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Show cameras on screen | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/display/cameraview/cameraview.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/cameras.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#show-several-cameras-at-once)† | [✅](https://github.com/dinki/View-Assist/blob/main/View%20Assist%20dashboard%20and%20views/views/camera/advancedcamera.yaml) |
| go2rtc camera import | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/cameras.md#add-a-go2rtc-server) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#cameras-from-go2rtc)† | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/view-camera) |
| Camera sound | —† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/media/rtc/viewer.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/cameras.md#sound)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#4-cameras)† | ❔ |
| Device camera as an HA camera | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/vision/vision.go) | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/camera.md#home-assistant)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5) | [🚧](https://github.com/msp1974/ViewAssist_Companion_App/commit/92966ee1dd634061653a55d80b32011d74e29842)† |
| Device camera as RTSP/ONVIF | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.10/internal/feature/rtspd/rtspd.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.37/docs/camera.md#motion-events)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#recording-this-devices-camera)† | [🚧](https://github.com/msp1974/ViewAssistCompanionApp/pull/54)† |
| Camera preview on the device | —† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/mirror.go)† | —† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#camera-preview)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md) | [🟡](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/ui/layouts/CameraStreamLayout.kt)† |
| Camera presence, face and hand detection | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/camera.md#motion-detection)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#presence-and-gestures)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Configuration-Options)† |
| **Communication** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| HA announcements | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/assistant/turn/announce.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/CHANGELOG.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#announcements) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/voice/announce.go#L16-L19) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/assist_satellite.py) |
| Intercom between devices | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#drop-in)† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/call) | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md#calling-another-device-in-the-house)† | [❌](https://dinki.github.io/View-Assist/docs/wishlist)† |
| Broadcast to every device | ❔ | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/feature/broadcast/broadcast.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/CHANGELOG.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md#announce-to-all) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#announce-to-the-house) | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/broadcast)† |
| Auto-answer (Drop In) | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#drop-in)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/call.go)† | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md#a-call-coming-in) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md#calling-another-device-in-the-house)† | [❌](https://dinki.github.io/View-Assist/docs/wishlist)† |
| Call by voice | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/blueprints/automation/drop_in.yaml)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/media/call/action.go)† | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md#calling-by-voice)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md#calling-a-contact-by-voice)† | [❌](https://dinki.github.io/View-Assist/docs/wishlist)† |
| Video calls between devices | —† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/call/screen.go)† | —† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md) | ❔ | [❌](https://dinki.github.io/View-Assist/docs/wishlist)† |
| Phone calls | ❔ | ❔ | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md)† | ❔ |
| **Sensors and controls** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Hardware buttons as HA events | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#buttons-and-light-ring)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/feature/buttons/buttons.go#L19-L24)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/echo-dot.md#the-buttons)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/buttons/buttons.go)† | ❔ |
| Mic mute reported to HA | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant) | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/mute) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#home-assistant-entities-and-actions)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/mute/mute.go) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py) |
| Light sensor in HA | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/light/light.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_esphome.py)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#controls) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/diag/diag.go) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/sensor.py) |
| Bluetooth proxy | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#bluetooth-proxy)† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/bluetooth/proxy/proxy.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#bluetooth-proxy) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md#known-gaps)† | ❔ |
| LED ring control from HA | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/3af8987e5e5ed07529a23c58566167bea829ab9d/docs/GUIDE.md#buttons-and-light-ring)† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/hardware/led)† | [❔](https://github.com/wilbowes/EchoMuse/issues/66)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/docs/led-control.md#home-assistant)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/light/light.go)† | ❔ |
| LED ring styles | [🚧](https://github.com/Gamer92000/echo-dot-assist/blob/a4fe0bdb6ac6914b1a02cda9f4a1a4f4ace94750/docs/GUIDE.md#buttons-and-light-ring)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/ring.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/led-control.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#7-voice)† | ❔ |
| Touch gestures | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/input/touch/gesture.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/gestures.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#presence-and-gestures)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/event.py) |
| Clap detection | ❔ | ❔ | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/gestures.md#claps) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#presence-and-gestures)† | ❔ |
| Proximity sensor | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/screensavers.md#proximity-detection)† | [❔](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/hardware.md)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/event.py) |
| Room loudness sensor | ❔ | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/feature/diag/diag.go#L381-L382)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#ambient-noise) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#how-loud-the-room-is)† | ❔ |
| Camera shutter state | —† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/feature/privacy) | —† | [❌](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/camera.md#hardware-notes)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/diag/diag.go) | ❔ |
| Do not disturb | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/call.go)† | ❔ | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md#a-call-coming-in)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md#calling-another-device-in-the-house) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/switch.py) |
| Device health readings | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/diag/diag.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#diagnostics) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/diag/diag.go)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/sensor.py)† |
| Sound event detection | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#sound-detection)† | ❔ | ❔† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#ambient-noise)† | [❔](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/standalone-plan.md)† | ❔ |
| Wi-Fi motion sensing | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#wi-fi-motion)† | ❔ | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md) | ❔ | ❔ |
| **Home Assistant** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| ESPHome native API link | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/README.md#installing) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#setup) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#after-installing-every-device) | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/manifest.json)† |
| Settings as HA entities | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/libcountertop/tree/b9aa28bf371a/pkg/settings) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#configuration)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#under-the-hood) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Configuration-Options) |
| Custom HA actions | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#drop-in)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/docs/src/content/docs/use/entities.mdx#L154-L169)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#notifications) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md) | [✅](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/services.yaml) |
| Events on the HA bus | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/docs/src/content/docs/use/entities.mdx#L144-L152)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/events.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#voice-request-events) | [✅](https://dinki.github.io/View-Assist/docs/developer-resources/timers) |
| Change the view from HA | —† | [🟡](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/tessera/alerts.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#controls)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#show-or-hide-the-dashboard)† | [✅](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/services.yaml) |
| Launch Android apps | —† | —† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#launch-an-app) | —† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/releases/tag/v0.9.2)† |
| Time and time zone from HA | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/timesync/timesync.go) | [❔](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/emos/README.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md)† | [🟡](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/js_modules/view_assist.js)† |
| Voice timers | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#voice) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/timer/timer.go) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/voice-satellite.md#timers-announcements-and-conversations) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#start-a-timer) | [✅](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/alarms-reminders-timers)† |
| Alarms | ❔ | [✅](https://github.com/ygelfand/libcountertop/blob/4683e2463a07/pkg/assistant/alarm/actions.go)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/alarms.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#set-an-alarm) | [🟡](https://dinki.github.io/View-Assist/docs/developer-resources/timers)† |
| Reminders | ❔ | ❔ | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/alarms.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#set-a-reminder) | [🟡](https://dinki.github.io/View-Assist/docs/extend-functionality/sentences/alarms-reminders-timers)† |
| Sunrise wake light | ❔ | [✅](https://github.com/ygelfand/echolocal/blob/0.0.10/internal/feature/alarm/alarm.go)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/alarms.md#sunrise) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#sounds-of-your-own) | ❔ |
| Keeps working when HA is down | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#drop-in)† | ❔ | [❌](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/alarms.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/releases/tag/v0.13.5)† |
| Control a group of devices | ❔ | [✅](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.11/src/tabs/groups.ts)† | ❔ | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/blueprints/script/techo5/stop_every_device.yaml)† | ❔ |
| **Management** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Over-the-air updates | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#updating)† | [✅](https://github.com/ygelfand/echolocal/tree/0.0.9/internal/update) | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/faq.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/updates.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#after-installing-every-device) | [✅](https://github.com/msp1974/ViewAssist_Companion_App/releases/tag/v0.4.0)† |
| Update channels | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#updating)† | [✅](https://github.com/ygelfand/libcountertop/blob/b9aa28bf371a/pkg/settings/schema/update.go) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller-ea/DOCS.md)† | [❌](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/app/lib/managers/update/update_manager.dart)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#after-installing-every-device)† | [🟡](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/services.yaml)† |
| Rollback after a failed update | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#updating) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/update/script.go) | [✅](https://github.com/wilbowes/EchoMuse/releases/tag/emos-v0.10) | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/updates.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5)† | [🟡](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/update.py)† |
| Signed updates | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md) | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/update/fetch.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_api.py)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/updates.md#custom-repository)† | [✅](https://github.com/HuskerMinion/techo5-dot/blob/dot-v1.0.31/README.md#L82-L84) | [❔](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/utils/Updater.kt) |
| Web settings page | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.11/internal/feature/frontend/frontend.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#remote-ui) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md) | [❌](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Settings-Menu)† |
| Log viewer | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [✅](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.11/src/logs.ts)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/static/dashboard.jsx) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#rest-surface) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#11-the-remote-api)† | ❔ |
| Remote screenshot | —† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/runtime/webapi/screen.go)† | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#rest-surface) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md#working-on-it)† | ❔ |
| Diagnostics download | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/runtime/webapi/developer.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/support-bundle.md) | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#rest-surface)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#questions-and-problems) | [🚧](https://github.com/msp1974/ViewAssist_Companion_App/pull/160)† |
| Remote shell | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#adb-over-wi-fi)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/config/diag.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/static/dashboard.jsx)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.38/docs/esphome.md#run-a-shizuku-command)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#set-the-ssh-authorized-keys)† | ❔ |
| Remote API | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/src/hassmic/web.c)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/runtime/webapi/webapi.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_api.py) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#11-the-remote-api)† | [🟡](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/core/websocket.py)† |
| Copy settings between devices | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/sync.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/fleet.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#sharing-settings-with-the-other-devices)† | [🟡](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration)† |
| Settings export and import | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [🟡](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/runtime/webapi/portable.go#L41-L60)† | ❔ | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#rest-surface) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#10-saving-the-settings) | ❔ |
| Change Wi-Fi without reinstalling | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/cli/echolocal/wifi.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/gestures.md#the-actions)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md)† | —† |
| Restart or reboot remotely | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/reboot/reboot.go) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/static/dashboard.jsx)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#controls)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#whats-new) | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/blob/v0.13.5/custom_components/vaca/button.py)† |
| Automatic recovery after a crash | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/scripts/system/main.sh)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/parts/echolocal.rc)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/emos/README.md)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/kiosk.md#staying-in-front) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/overview.md#working-on-it)† | [✅](https://github.com/msp1974/ViewAssist_Companion_App/releases/tag/v0.8.0) |
| Recovery and way back to stock | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#undo-it)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.11/docs/src/content/docs/start/before.mdx#L50)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/emos/README.md)† | [—](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/home-launcher.md#getting-back-out)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#going-back-to-twrp-or-fire-os) | —† |
| List of other devices | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page) | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/gui/calls.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/static/dashboard.jsx)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#kiosk-switcher) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#sharing-settings-with-the-other-devices)† | ❔ |
| Device naming | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/cli/echolocal/name.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_labels.py) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md#node-name) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/setup/rename.go) | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Changing-Device-UUID)† |
| Reset or leave HA | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#factory-reset) | ❔ | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/CHANGELOG.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/esphome.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/actions.md#disconnect-the-device-from-home-assistant-for-good) | [✅](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/ui/components/MenuComponent.kt)† |
| Central management server | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#settings-page)† | [🟡](https://github.com/ygelfand/echolocal-hacs/blob/v0.0.13/README.md#fleet)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/fleet.md)† | ❔ | [🟡](https://dinki.github.io/View-Assist/docs/view-assist-configuration/masterconfig-configuration)† |
| Browser-based installer | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#install)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/cli/echolocal/install.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/quickstart.md)† | [❌](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md#get-started)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/tools/install-show.py)† | [❌](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Getting-Started)† |
| **Privacy and security** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| Vendor cloud services removed | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md)† | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/android/services/packages.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/README.md)† | [—](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5) | —† |
| Inbound firewall | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md) | [✅](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/android/firewall/firewall.go) | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/emos/README.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/tls.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#after-installing-every-device) | ❔ |
| Signed or encrypted device-to-device traffic | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/network/trust/trust.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [🟡](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/intercom.md#remote-api)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/phone.md#calling-another-device-in-the-house)† | —† |
| Login for settings and admin | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/runtime/webapi/auth.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/controller/em_auth.py)† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/remote-api.md#authentication) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#8-settings-lock)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/wiki/Troubleshooting)† |
| Sendspin pairing | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#in-home-assistant) | [🚧](https://github.com/ygelfand/echolocal/pull/34)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/sendspin.md) | [❌](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/getting-started.md#after-installing-every-device)† | ❔† |
| HTTPS and certificate options | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/GUIDE.md#voice)† | [✅](https://github.com/ygelfand/libcountertop/blob/d51a3865a9e1/pkg/network/certwarden/certwarden.go)† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/tls.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/echod/internal/feature/diag/diag.go)† | [🟡](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/res/values/strings.xml)† |
| Mic and camera indicators | [🟡](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/README.md#on-the-echo)† | [🟡](https://github.com/ygelfand/echolocal/blob/0.0.9/internal/feature/livecam/livecam.go)† | [🟡](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/gestures.md#claps)† | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#recording-this-devices-camera)† | [🟡](https://github.com/dinki/view_assist_integration/blob/2026.7.0/custom_components/view_assist/js_modules/view_assist.js)† |
| No telemetry by default | [✅](https://github.com/Gamer92000/echo-dot-assist/blob/v2026.10.17.151309/docs/SECURITY.md) | ✅† | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/docs/configuration.md) | [❌](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/analytics.md)† | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/README.md#why-techo5)† | [❌](https://github.com/msp1974/ViewAssistCompanionApp/blob/3c9517c49af97794d3ae950c123a20400d2bb078/app/src/main/java/com/msp1974/vacompanion/utils/Logger.kt)† |
| Kiosk lockdown | —† | — | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/kiosk.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/setup.md#8-settings-lock)† | [🟡](https://github.com/msp1974/ViewAssist_Companion_App/releases/tag/v0.7.0)† |
| **Extensibility** | **echo-dot-assist** | **EchoLocal** | **EchoMuse** | **Kiosk Satellite** | **TECHO5** | **View Assist** |
| User-made screen pages | —† | ❔ | —† | [✅](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/custom-pages.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/dashboards.md#web-pages)† | [✅](https://dinki.github.io/View-Assist/docs/community-contributions/cc-views)† |
| Plugin system | ❔ | ❔ | ❔† | [✅](https://github.com/jxlarrea/kiosk-satellite-plugins) | ❔ | [🟡](https://dinki.github.io/View-Assist/docs/developer-resources)† |
| Wake word training | ❔ | ❔ | [✅](https://github.com/wilbowes/EchoMuse/blob/v2.18.1/oww_forge/README.md)† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/docs/custom-wake-words.md) | [🟡](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/training-wake-word.md)† | ❔ |
| Stream deck and computer control | —† | ❔ | —† | [❔](https://github.com/jxlarrea/kiosk-satellite/blob/2026.10.29/README.md) | [✅](https://github.com/HuskerMinion/techo5/blob/v1.6.3/docs/deck.md)† | ❔ |

## Notes to the table

**Basics**

- **Replaces the device's OS**: echo-dot-assist: keeps Fire OS; replaces only the Alexa client; EchoLocal: Fire OS kept; echod runs on it; EchoMuse: emOS default; FireOS-with-​root path also offered; Kiosk Satellite: app on the existing OS; TECHO5: Echos and Skylight: yes; the Panel app runs on Android; Show 8 gen 2 can also keep Amazon's kernel; View Assist: app on stock Android
- **Works with other display or microphone apps**: echo-dot-assist: wake word arbitration with Kiosk Satellite tablets; EchoLocal: renders Tessera Screen Manager tile pages; Kiosk Satellite: hosts the Voice Satellite card integration in its dashboard; TECHO5: Show, Skylight and Panel app can be a screen for the Tessera HA add-on (preview); View Assist: Wallpanel, Fully Kiosk, RAD, HassMic, Stream Assist, ESPHome
- **Interface languages**: echo-dot-assist: spoken Bluetooth announcement​s in 12 languages; settings page English; EchoLocal: language catalog, English only; EchoMuse: add-on option labels catalog, English only; Kiosk Satellite: 8 languages; TECHO5: date, forecast, weather in 8 languages; rest English; View Assist: HA config screens en/​de/​sr(/​tr)​; app screens English only
- **Porting to new hardware**: echo-dot-assist: per-model directories and porting steps; EchoLocal: per-board Go files; no written guide; EchoMuse: board profiles, profiling scripts, protocol doc; Kiosk Satellite: per-device guides and hardware notes, no porting guide; TECHO5: porting plans and hardware notes per device, no porting guide; View Assist: per-device setup notes only

**Voice**

- **Home Assistant Assist satellite**: EchoMuse: through the controller, as an ESPHome satellite; View Assist: through VACA app
- **Follow-up listening**: echo-dot-assist: ESPHome mode; View Assist: Continue conversation switch
- **HA can start a conversation**: echo-dot-assist: start_​conversation feature flag; ESPHome mode; EchoLocal: via ESPHome start_​conversation feature; TECHO5: start_​conversation feature flag in code
- **On-device wake word**: echo-dot-assist: Amazon's engine or microWakeWor​d; EchoLocal: microWakeWor​d and openWakeWord; EchoMuse: default for new installs; TECHO5: microWakeWor​d; View Assist: VACA app
- **Custom wake word models**: echo-dot-assist: microWakeWor​d models, experimental; EchoLocal: .tflite library through the HACS integration; TECHO5: most microWakeWor​d models, incl. ADD and pooling layers; a model needing an unsupported operation is refused
- **Two wake words, two assistants**: echo-dot-assist: ESPHome mode; EchoMuse: one model at a time (#112)
- **Wake word on a server**: echo-dot-assist: streams mic to HA's openWakeWord; EchoMuse: per-Echo option; streams to the controller; TECHO5: off by default; confirm-only mode also offered; View Assist: through third-party mic apps (Stream Assist), not VACA
- **Start a turn without the wake word**: echo-dot-assist: action button only; EchoLocal: Dot action button; "Talk now" on Shows; EchoMuse: action button; HA can also use start_​conversation; Kiosk Satellite: gesture, HA action or Android broadcast; TECHO5: screen tap, action button, HA Wake button; View Assist: HA wake button
- **Cancel a request in progress**: echo-dot-assist: action button or wake word; ESPHome mode; EchoLocal: action button or screen touch; EchoMuse: wake word interrupts while thinking or speaking; Kiosk Satellite: double tap or vs_cancel action; TECHO5: action button or HA Cancel request button; View Assist: wake word or "stop" interrupts the reply
- **Stop without the wake word**: echo-dot-assist: needs the wake word first ("Alexa, stop"); EchoLocal: ignored while listening; EchoMuse: timer stops by wake word then any speech; View Assist: microWakeWor​d stop model only
- **Nearest device answers**: echo-dot-assist: not with HA's wake word engine; EchoMuse: decided by the controller (loudest Echo); Kiosk Satellite: needs one network segment; off by default; TECHO5: on by default; needs same house word
- **Echo cancellation**: echo-dot-assist: Amazon's front end; EchoMuse: off by default; Kiosk Satellite: not for web page or DLNA video sound; View Assist: Android AEC where the device offers it
- **Noise suppression**: echo-dot-assist: RNNoise, off by default; EchoLocal: always on (Speex preprocessor​)​; no switch in 0.0.9; EchoMuse: off by default; runs on the controller; Kiosk Satellite: off by default; View Assist: VACA noise suppression level; off by default
- **Mic gain and leveling**: echo-dot-assist: target level, automatic gain
- **Mic array mixing**: echo-dot-assist: Amazon's front end, no user choice; EchoLocal: center, delay-and-su​m or vendor beamformer; Dot only; EchoMuse: Omni, Front, Rear or auto mic choice; Kiosk Satellite: downmix or one channel; multichannel USB arrays only; TECHO5: beamformer off on Show 8 gen 2
- **External or USB microphone choice**: EchoMuse: wake-word mic (center, MK1-MK6) and beam direction selectable; no USB mic; View Assist: USB, Bluetooth, wired
- **End of speech detected on device**: echo-dot-assist: Home Assistant's VAD decides; EchoLocal: max listen time only; EchoMuse: button and follow-up turns only; wake turns use HA VAD; Kiosk Satellite: HA's Finished speaking detection select; TECHO5: on-device VAD on/off switch; no timing setting found; View Assist: HA VAD sensitivity select
- **Speech right after the wake word**: EchoLocal: 250 ms pre-roll sent only with wake sound off; Kiosk Satellite: setting skips the wake sound; TECHO5: listens after wake sound; wake sound can be None; open PR #217 would keep speech said in one breath with the word; View Assist: mic stays open, gain lowered during wake sound
- **Replies start while being generated**: EchoLocal: "Stream" option; default is download first; EchoMuse: off by default; needs streaming agent and TTS; Kiosk Satellite: uses HA's streaming TTS; TECHO5: Reply delivery setting: whole file or streamed; View Assist: HA Wyoming TTS streaming; depends on agent and TTS engine
- **Saved voice recordings**: EchoMuse: off by default; last 10 turns per device; Kiosk Satellite: last 10 seconds replay; one TTS capture for diagnostics; View Assist: last 15 requests replayable in overlay; recording off by default
- **Voice-turn history**: EchoLocal: in the HACS integration; EchoMuse: transcript, scores, timings; reply text not stored; TECHO5: last-turn sensors, kept in HA history; View Assist: diagnostics overlay log of last 15 turns; overlay off by default
- **Last-turn sensors in HA**: echo-dot-assist: Wake word event entity (detected, no speech) on main and beta; no transcript; EchoMuse: not among the HA entities exposed; Kiosk Satellite: state sensor only; View Assist: STT and TTS sensors
- **Wake-word clips and near misses**: echo-dot-assist: near_miss events without audio, Amazon's engine; main and beta, off by default; EchoLocal: open PR #136 (opt-in wake audio captures, 24 hours, 100 clips); needs library PR #2; EchoMuse: off by default; near-miss counter too; Kiosk Satellite: off by default; TECHO5: off by default
- **Uses the stock audio front end**: EchoLocal: vendor beamformer is a mixing option; Pryon in draft PR; EchoMuse: Amazon audio blobs avoided by design; Kiosk Satellite: Amazon's wake word engine is an option on the Echo Dot image (2026.​10.​34)​; own engines elsewhere; firmware mic processing kept where present; TECHO5: Dot: Amazon beamformer re-implement​ed from vendor coefficients​, optional; View Assist: own wake engines
- **Whisper detection**: echo-dot-assist: needs Amazon's model download; ESPHome mode
- **Realtime speech-to-speech AI**: Kiosk Satellite: OpenAI, xAI or Gemini key; HA control via MCP server; TECHO5: off by default; paid cloud; HA control via MCP token
- **Assistant without Home Assistant**: EchoLocal: on-device keyword commands only ("radar", "mirror"); Shows only; EchoMuse: "Not standalone"; needs HA Assist; Kiosk Satellite: cloud realtime model skips HA pipeline; setup still uses HA; TECHO5: needs own Wyoming and chat-model servers; no house control; View Assist: needs HA pipeline
- **Library of voice-command add-ons**: echo-dot-assist: one Drop In blueprint; Kiosk Satellite: four blueprints: voice alarms, voice calls, calendar and music; TECHO5: 10 voice blueprints (alarms, reminders, volume, calls, help) plus 11 event blueprints and 2 scripts, with import buttons; View Assist: blueprints, pick and choose
- **Music by voice**: EchoMuse: mentioned via HA/Music Assistant; no guide; Kiosk Satellite: blueprint for an LLM agent; plays on the kiosk's speaker, best with Music Assistant; TECHO5: through Music Assistant; View Assist: through Music Assistant blueprint

**Audio and media**

- **HA media player**: Kiosk Satellite: through DLNA renderer or Sendspin via Music Assistant; View Assist: VACA media player
- **Sendspin / Music Assistant player**: EchoLocal: off by default; EchoMuse: mono; TECHO5: on by default; View Assist: VACA integration dev branch has a Sendspin switch and server selector (unreleased)​; no Sendspin player found in the app repo
- **Bluetooth speaker for phones**: echo-dot-assist: SBC, AAC, aptX, aptX HD; off by default; EchoLocal: Shows only; TECHO5: Show, Spot, Dot and Echo gen 2; the Panel app has no Bluetooth
- **Play on a Bluetooth speaker**: echo-dot-assist: SBC only, one speaker; Kiosk Satellite: Speaker picker lists Bluetooth outputs; media follows Android routing; TECHO5: Echos; not the Panel app (no Bluetooth)
- **Headphone or line-out jack**: echo-dot-assist: stock 3.5 mm jack plays; EQ applies, speaker gain does not; EchoLocal: Dot jack; headphones sensor; EchoMuse: mono; stereo in progress (PR#753); Kiosk Satellite: Speaker picker lists wired outputs; media follows Android routing; TECHO5: devices with a jack only (Spot, Dot, Dot gen 3, Echo gen 2 mono)
- **AirPlay receiver**: EchoLocal: Shows only, with screen mirroring; off by default; Kiosk Satellite: AirPlay 1, audio only; TECHO5: Show and Dot only; off by default
- **DLNA renderer**: TECHO5: off by default; no seeking yet
- **Google Cast receiver**: EchoLocal: off by default; audio only on the Dot; TECHO5: video.md: not a Google Cast target
- **Separate volumes per stream**: echo-dot-assist: Separate music, voice, sounds, timer and call volumes in the 2026-10-21 beta (PR #47 merged); not in a stable release yet; EchoMuse: reply level offset (+6/+12 dB) over volume; Kiosk Satellite: media, assistant, intercom, notification​, alarm; TECHO5: separate alarm/timer ring volume only; View Assist: voice, music, alarm
- **Equalizer**: echo-dot-assist: bass, mid, treble; EchoLocal: speaker EQ on/off switch; EchoMuse: 8 bands with presets; TECHO5: bass and treble ±6 dB
- **Speaker gain boost**: echo-dot-assist: +12 dB Dot 2/Echo 2, +2 dB Dot 3; EchoMuse: volume capped at codec unity gain by design; TECHO5: Show 5 gen 2, Show 8 gen 2, Dot and Echo 2nd gen; not the other Shows or the Spot
- **Own sounds and tones**: EchoLocal: upload through the HACS integration; View Assist: wake sounds and alarms
- **Voice audio on another media player**: View Assist: satellite maps any HA media player
- **Internet radio**: EchoLocal: needs Radio Browser in HA; Shows only; EchoMuse: only through the HA media player; Kiosk Satellite: radio only through Music Assistant or another player; View Assist: through Music Assistant blueprint
- **Sleep and ambient sounds**: TECHO5: generated on the device; View Assist: Sound Machine blueprint, user-supplie​d files
- **Sleep timer**: View Assist: duration option on Sound Machine
- **Now playing screen**: echo-dot-assist: released models have no screen; EchoLocal: Shows only; EchoMuse: no screen; View Assist: music view
- **Show and control another player**: echo-dot-assist: released models have no screen; EchoMuse: no screen; View Assist: the mapped music player
- **Synced lyrics**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: Shows only; off by default; LRCLIB
- **Video playback on screen**: echo-dot-assist: released models have no screen; EchoLocal: from cast apps; cast off by default; Shows only; EchoMuse: no screen; Kiosk Satellite: via DLNA renderer; TECHO5: Show, Spot and Skylight; off by default; H.264; H.265, VP8 and VP9 on Show 8 gen 2 and Skylight

**Screen**

- **Home Assistant dashboard on screen**: echo-dot-assist: Echo Show 8s, via the optional Kiosk Satellite app (open PR #58, untested on a device per its description); EchoLocal: own HA tiles and Tessera pages; EchoMuse: no screen; TECHO5: drawn on device, or streamed via separate dashcast server
- **Other web pages on screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: needs dashcast server; View Assist: webpage view by voice or navigate
- **Settings on the device screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; View Assist: most settings are in HA
- **App drawer or launcher on screen**: echo-dot-assist: released models have no screen; EchoLocal: built-in apps only; EchoMuse: no screen; Kiosk Satellite: kiosk menu and App Launcher; TECHO5: fixed drawer tabs (Cameras, Radio, Call); View Assist: configurable launch icons and toggle menu open views
- **Return to home view**: echo-dot-assist: released models have no screen; EchoLocal: Tessera home page after the screen saver; EchoMuse: no screen; View Assist: view timeout
- **Screen orientation**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: set in the app
- **Keep screen on**: echo-dot-assist: released models have no screen; EchoLocal: screen saver delay "Never"; EchoMuse: no screen
- **UI scale**: echo-dot-assist: released models have no screen; EchoLocal: UI size and keyboard size; EchoMuse: no screen; View Assist: web zoom and text size
- **Clock faces**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: Digital, Flip, Roller, Hand; TECHO5: 15 styles, incl. Nixie, Stack and Overlap; bottom left, center or right position
- **Weather display**: echo-dot-assist: released models have no screen; EchoLocal: current conditions; forecast only in Tessera tiles; EchoMuse: no screen
- **Screensaver**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: photos, dashboard or web page; View Assist: black-screen switch; moving clock is a community view
- **Photo slideshow**: echo-dot-assist: released models have no screen; EchoLocal: Immich only; EchoMuse: no screen; View Assist: community view; rotating backgrounds
- **Immich photos**: echo-dot-assist: released models have no screen; EchoLocal: needs Immich 3.2 or newer; EchoMuse: no screen; View Assist: community tip
- **Entity chips on the idle screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: Shows only; View Assist: status icons
- **HA notifications on screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; View Assist: alert or info view
- **Timers on screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; View Assist: alarm view
- **Calendar on screen**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: HA calendars in Day, Week, Month and Schedule views; Up next widget; Calendar screensaver mode; TECHO5: HA calendars or iCal links
- **To-do and shopping lists**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: only via an HA dashboard card; View Assist: items added by voice
- **Answers shown as views**: echo-dot-assist: released models have no screen; EchoLocal: shows what was said and the reply; EchoMuse: no screen; Kiosk Satellite: result panels need an admin token; TECHO5: forecast, radar, camera pages for voice requests
- **Display modes**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: hold, view rotation, lockdown, Now Playing, screensaver mode; TECHO5: night mode switch and dashboard show/hide
- **Weather radar and alerts**: echo-dot-assist: released models have no screen; EchoLocal: alerts US only (NWS), off by default; EchoMuse: no screen; TECHO5: alerts U.S. only
- **Night mode**: echo-dot-assist: released models have no screen; EchoMuse: no screen; Kiosk Satellite: Clock screensaver; needs a light sensor; View Assist: night mode set by voice or automation; experimental
- **Automatic brightness**: echo-dot-assist: no screen; light ring follows room light; EchoMuse: no screen; Kiosk Satellite: device sensor or HA entity; TECHO5: light sensor uncalibrated on Show 8 gen 2; View Assist: Android adaptive brightness switch
- **Themes**: echo-dot-assist: released models have no screen; EchoLocal: 18 themes, 9 styles, plus custom themes; EchoMuse: no screen; Kiosk Satellite: light/dark only; TECHO5: dark, light or auto by room light; custom colors; skins (Pip-Boy, LCARS and more); View Assist: dark mode
- **Fonts**: echo-dot-assist: released models have no screen; EchoLocal: one style uses a mono font; size setting; EchoMuse: no screen; Kiosk Satellite: clock, widgets, At a Glance; View Assist: font name typed in config
- **Screen on/off and brightness from HA**: echo-dot-assist: Echo Show light band only, branch checkers; EchoLocal: brightness only; no screen on/off entity found; EchoMuse: no screen; Kiosk Satellite: screen-off needs Device admin

**Cameras**

- **Show cameras on screen**: echo-dot-assist: released models have no screen; EchoLocal: Cameras app: snapshot grid, live video one camera at a time; Shows only; EchoMuse: no screen; TECHO5: grid of up to six on the Show
- **go2rtc camera import**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: go2rtc camera sound not played yet
- **Camera sound**: echo-dot-assist: released models have no screen; EchoLocal: HA camera WebRTC audio in the live view, through the HACS integration; Shows only; EchoMuse: no screen; Kiosk Satellite: single camera only; off by default; TECHO5: HA stream or RTSP; not go2rtc or Reolink recorder
- **Device camera as an HA camera**: echo-dot-assist: released models have no camera; EchoMuse: no camera; Kiosk Satellite: off by default; View Assist: VACA integration dev branch has an RTSP camera entity (unreleased)​; app side in open PR #54
- **Device camera as RTSP/ONVIF**: echo-dot-assist: released models have no camera; EchoLocal: RTSP with ONVIF discovery and auth; off by default; EchoMuse: no camera; Kiosk Satellite: ONVIF motion events, off by default; no PTZ or ONVIF snapshots; TECHO5: off by default; RTP/JPEG, not H.264; ONVIF discovery switch, off by default; login settable from HA (opt-in); View Assist: RTSP PR #54 open; integration dev branch has the matching camera entity
- **Camera preview on the device**: echo-dot-assist: released models have no camera; EchoLocal: Mirror app; EchoMuse: no camera; Kiosk Satellite: brief preview after a face wake; View Assist: preview for checking detection
- **Camera presence, face and hand detection**: echo-dot-assist: released models have no camera; EchoMuse: no camera; Kiosk Satellite: camera off by default; TECHO5: motion, person with confidence sensor; palm-over-le​ns stop; no face/hand detection; off by default; View Assist: motion or face; front camera needed, off by default

**Communication**

- **HA announcements**: echo-dot-assist: DND drops them
- **Intercom between devices**: echo-dot-assist: Drop In, peer to peer, encrypted; EchoMuse: no calling or Drop In; idea in #555; TECHO5: needs same house word; View Assist: on wishlist
- **Broadcast to every device**: EchoLocal: records until you stop talking, plays on every device; EchoMuse: only through HA announcement​s the user sends; View Assist: Broadcast blueprint
- **Auto-answer (Drop In)**: echo-dot-assist: connects at once by default; EchoLocal: off by default; from everyone or chosen devices; EchoMuse: no Drop In; TECHO5: Drop In off by default; View Assist: no intercom
- **Call by voice**: echo-dot-assist: shipped blueprint, six languages; EchoLocal: built-in "call [the] {device}" voice trigger through the HACS integration; EchoMuse: no calling; Kiosk Satellite: needs an LLM agent and admin token; TECHO5: built in: the device's phone book handles 'call Mom'; a blueprint covers 'call the kitchen' and HA contacts; View Assist: no intercom
- **Video calls between devices**: echo-dot-assist: released models have no camera; EchoLocal: Shows video; Dot audio only; EchoMuse: no camera; View Assist: no intercom
- **Phone calls**: EchoMuse: no calling; TECHO5: needs own SIP provider; off until signed in

**Sensors and controls**

- **Hardware buttons as HA events**: echo-dot-assist: press and 1 s hold events; EchoLocal: press and hold on every button; double and triple tap on the Dot action button; EchoMuse: action button tap, multi-tap, hold; Kiosk Satellite: Echo Dot Action button via gesture "Fire an event"; TECHO5: press, hold, long hold events
- **Mic mute reported to HA**: Kiosk Satellite: VS Mute switch
- **Light sensor in HA**: echo-dot-assist: off by default; EchoMuse: on Dots that have one
- **Bluetooth proxy**: echo-dot-assist: up to 3 connections; ESPHome mode; EchoLocal: passive scan and raw advertisemen​ts only; EchoMuse: active connections off by default; 3 max; TECHO5: scanning only; off by default; active connections in PR #184
- **LED ring control from HA**: echo-dot-assist: color, brightness, effects; fill and LED by LED colors on main and beta (issue #57); EchoLocal: Dot only; EchoMuse: HA ring control asked in #66 and #295; no code; Kiosk Satellite: idle lights only: a light (on/off, brightness, color) and a Solid/​Breathing select; other states are set on the device; TECHO5: Dot, Echo gen 2 and Dot gen 3
- **LED ring styles**: echo-dot-assist: ring designs and theme packs on main and beta (issue #38); release has stock states; EchoLocal: Dot only; EchoMuse: five scenes incl. custom colors; Kiosk Satellite: Echo Dot only; Echo-style states, colors adjustable; TECHO5: Wake word, Thinking and Replying effects, per wake word
- **Touch gestures**: echo-dot-assist: released models have no touchscreen; EchoLocal: navigation gestures only; EchoMuse: no touch surface; TECHO5: hold-on-cloc​k event off by default
- **Clap detection**: TECHO5: off by default
- **Proximity sensor**: echo-dot-assist: released models have no proximity sensor; EchoMuse: no proximity sensor; Kiosk Satellite: few kiosk tablets have the sensor; TECHO5: Show 5 light chip has no proximity half; other models not documented
- **Room loudness sensor**: EchoLocal: room level and noise floor sensors in dBFS; also drives the ring reaction; TECHO5: off by default
- **Camera shutter state**: echo-dot-assist: released models have no camera; EchoMuse: no camera; Kiosk Satellite: closed shutter looks like no camera
- **Do not disturb**: echo-dot-assist: drops announcement​s, refuses Drop In; EchoLocal: "Incoming calls" switch refuses calls only; Kiosk Satellite: intercom only
- **Device health readings**: echo-dot-assist: SoC temperature and CPU on settings page; EchoMuse: dashboard status page, not HA sensors; TECHO5: Wi-Fi signal, load, memory, free space; View Assist: battery; device info screen
- **Sound event detection**: echo-dot-assist: Amazon's model, off by default; ESPHome mode; EchoMuse: requested in issue #746; Kiosk Satellite: loudness level only; TECHO5: smoke alarm listed only as future research
- **Wi-Fi motion sensing**: echo-dot-assist: experimental​, off by default

**Home Assistant**

- **ESPHome native API link**: EchoMuse: voice satellite API unencrypted (#341); View Assist: custom HA integrations
- **Settings as HA entities**: echo-dot-assist: many settings on the settings page only; EchoMuse: wake word select only; Kiosk Satellite: about 30 settings exposed, of several hundred
- **Custom HA actions**: echo-dot-assist: Drop In and arbitration key actions; light_​ring_​leds on main and beta; EchoLocal: show_​message, call, broadcast, alarms, tones, recordings, logs, settings export and import, Tessera
- **Events on the HA bus**: echo-dot-assist: button, Drop In result and sound events; EchoLocal: button events (action button also double and triple), turn events, alarm events, Tessera alerts, update outcome; EchoMuse: button events only
- **Change the view from HA**: echo-dot-assist: released models have no screen; EchoLocal: Tessera show_page only; EchoMuse: no screen; Kiosk Satellite: Dashboard view select; TECHO5: dashboard, forecast, radar, calendar, camera actions
- **Launch Android apps**: echo-dot-assist: no Android apps on released models; EchoLocal: no Android app layer exposed; EchoMuse: no screen; TECHO5: no Android on the device; View Assist: from dashboard links
- **Time and time zone from HA**: EchoMuse: controller sets the device clock; TECHO5: time zone from HA; clock from NTP, or from HA when no time server answers; View Assist: views offset to HA time; device clock untouched
- **Voice timers**: View Assist: through View Assist timer blueprint
- **Alarms**: EchoLocal: repeat days and snooze; set through HA actions; View Assist: voice alarms with snooze; no repeat days
- **Reminders**: View Assist: through blueprint
- **Sunrise wake light**: EchoLocal: screen dawn view or ring glow before the alarm
- **Keeps working when HA is down**: echo-dot-assist: Drop In needs no HA; offline use not documented; EchoMuse: "Not standalone"; Kiosk Satellite: alarms and intercom need no HA; TECHO5: alarms, intercom, announcement​s, go2rtc, doorbell; View Assist: satellite keeps running 5 minutes for music
- **Control a group of devices**: EchoLocal: in the HACS integration; TECHO5: stop-all or mute-all blueprint script and a dashboard script, for every TECHO5 device (not a chosen group)

**Management**

- **Over-the-air updates**: echo-dot-assist: ESPHome mode; off by default; EchoMuse: firmware and emOS, from the controller dashboard; View Assist: app update prompt; asset update entities
- **Update channels**: echo-dot-assist: release and beta; EchoMuse: channel set by which add-on is installed; Kiosk Satellite: pre-releases never offered; TECHO5: Echos; the Panel app has no Stable or Dev channel; View Assist: assets from dev branch via action
- **Rollback after a failed update**: Kiosk Satellite: downgrades refused; TECHO5: Echos (A/B slots); the Panel app has no A/B slots; View Assist: manual; asset backup option
- **Signed updates**: EchoLocal: SHA-256 hash check only; EchoMuse: md5 transfer check only; signing is issue #628; Kiosk Satellite: Android APK signature check
- **Web settings page**: EchoLocal: HTTPS page on port 443: every setting, alarms, calls, broadcasts, logs; EchoMuse: controller dashboard; View Assist: settings via HA entities and device menu
- **Log viewer**: EchoLocal: in the HACS card and as a download on the device web page; TECHO5: signed API log stream, SSH, diagnostics download
- **Remote screenshot**: echo-dot-assist: released models have no screen; EchoLocal: live screen view on the web page and /​api/​v1/​screen; Shows only; EchoMuse: no screen; TECHO5: off by default
- **Diagnostics download**: echo-dot-assist: log download, Sendspin token blanked; EchoLocal: diagnostics JSON from /​api/​v1/​diagnostics; secrets show only as "set"; Kiosk Satellite: raw logs only; View Assist: open PR
- **Remote shell**: echo-dot-assist: adb over Wi-Fi, 30 min, button-appro​ved; EchoLocal: ADB over network, off by default, unauthentica​ted; EchoMuse: admin console in the dashboard; Kiosk Satellite: one command at a time through Shizuku, from an HA action or the remote API; no interactive shell; TECHO5: SSH keys only; off by default
- **Remote API**: echo-dot-assist: the settings page's signed API, not documented separately; EchoLocal: /api/v1 with the admin password, a session or a token; TECHO5: off by default; everyday settings only; View Assist: through HA's API and entities
- **Copy settings between devices**: EchoLocal: pairing code on the device; Fleet tab in the HACS integration; EchoMuse: fleet defaults with per-section override; TECHO5: sharing off by default; View Assist: master config defaults
- **Settings export and import**: EchoLocal: export and import actions pass settings as JSON; no file save or restore screen
- **Change Wi-Fi without reinstalling**: EchoLocal: CLI over USB; EchoMuse: rolls back if the controller is unreachable; Kiosk Satellite: left to Android settings; TECHO5: Dot: wifi-set over SSH or USB tool; View Assist: stock Android Wi-Fi settings
- **Restart or reboot remotely**: EchoMuse: no button; reboot through the dashboard console; Kiosk Satellite: device reboot needs device owner or Shizuku; View Assist: webview reload from HA only
- **Automatic recovery after a crash**: echo-dot-assist: boot script keeps hassmic running; EchoLocal: init restarts echod; EchoMuse: emOS supervisor respawns; firmware slot fallback; TECHO5: supervisor restarts daemon; slot fallback
- **Recovery and way back to stock**: echo-dot-assist: uninstall, reflash stock from TWRP; EchoLocal: TWRP is the recovery; docs say to restore Fire OS with the XDA guide, no uninstaller; EchoMuse: TWRP, boot image restore, manual return to stock; Kiosk Satellite: app on a stock OS; View Assist: app on stock OS
- **List of other devices**: EchoLocal: intercom device list, no links; EchoMuse: controller dashboard lists all Echoes; TECHO5: other devices listed for settings sharing, no links
- **Device naming**: echo-dot-assist: fixed generated name; rename in HA only; EchoLocal: rename by re-running installer; HA sees new device; View Assist: UUID change needs re-adding device
- **Reset or leave HA**: EchoMuse: delete in dashboard; HA entry stays (#97); View Assist: Unpair device
- **Central management server**: echo-dot-assist: any Echo's page copies settings and models to the others; EchoLocal: HACS integration: Fleet compare and sync, groups, tones, health; EchoMuse: required; Kiosk Satellite: leader kiosk, no separate server; View Assist: HA integration
- **Browser-based installer**: echo-dot-assist: guided script on a Linux PC; EchoLocal: one-command PC installer; EchoMuse: unlock first needs firebreak on a PC; Kiosk Satellite: APK sideload; browser only finishes setup; TECHO5: one-command Python installer on a PC; View Assist: HACS and APK sideload

**Privacy and security**

- **Vendor cloud services removed**: echo-dot-assist: Alexa stopped; Amazon traffic firewalled to LAN; EchoLocal: Amazon packages disabled; EchoMuse: emOS removes Android; FireOS path disables Alexa; Kiosk Satellite: app on a stock OS; View Assist: app on stock OS
- **Inbound firewall**: EchoMuse: emOS only
- **Signed or encrypted device-to-device traffic**: echo-dot-assist: Kiosk arbitration mode is unkeyed; EchoLocal: with a home key set: TLS 1.3 between devices, plain-HTTP peer routes refused; calls over WebRTC; EchoMuse: all traffic goes through the controller over TLS once paired; Kiosk Satellite: intercom and fleet keyed; arbitration UDP not; TECHO5: signed with house word; intercom encrypted; View Assist: no device-to-de​vice traffic
- **Login for settings and admin**: echo-dot-assist: action-butto​n approval, one pending login at a time; EchoLocal: web admin password, 5 failed tries per 10 minutes; on-screen settings PIN with retry backoff; EchoMuse: login and button approval; no rate limit found; TECHO5: setup page needs on-device Allow; PIN and API lockouts; View Assist: pairs to first HA server; no login or rate limit
- **Sendspin pairing**: EchoLocal: open PR #34; TECHO5: docs: no key, no pairing, by design; View Assist: Sendspin itself in progress
- **HTTPS and certificate options**: echo-dot-assist: switch to turn checks off; EchoLocal: certificate checks on by default; own HTTPS certificate from Cert Warden; TECHO5: only a skip-certifi​cate-checks switch; View Assist: accept prompt for certificate errors
- **Mic and camera indicators**: echo-dot-assist: light ring shows listening and mic off; no camera; EchoLocal: camera off with mute; marks show mute, not use; EchoMuse: red mute ring and listening ring; no camera; Kiosk Satellite: relies on Android's own mic indicator; TECHO5: camera off with mute; Show's camera dot for RTSP and camera pages, not HA views; View Assist: listening overlay; no camera indicator
- **No telemetry by default**: EchoLocal: none found in code; Kiosk Satellite: analytics on by default, can be turned off; TECHO5: none found; internet use listed in README; View Assist: Firebase Analytics and Crashlytics in the app; no off switch found
- **Kiosk lockdown**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: settings PIN only; View Assist: launcher role only

**Extensibility**

- **User-made screen pages**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: web pages via dashcast server; Tessera pages (preview), grid of your choosing; View Assist: custom views
- **Plugin system**: EchoMuse: boundary proposed in issue #847; View Assist: custom views, blueprints, overlays
- **Wake word training**: EchoMuse: separate oww_forge container; TECHO5: written guide
- **Stream deck and computer control**: echo-dot-assist: released models have no screen; EchoMuse: no screen; TECHO5: Shows only

## Also does

Released features that no row covers, as each project's reviewer picked them.

**echo-dot-assist**

- Wi-Fi setup from a phone over BLE (Improv)
- Downloads Amazon's extra wake words and models on the device
- Copies wake word and detection models between Echos
- Wyoming satellite mode as an alternative to ESPHome
- Removes the wake tone from audio sent to HA (experimental)
- Keeps a Bluetooth speaker awake with an inaudible tone
- Answers ping from private addresses for HA's Ping integration
- Identify button: rainbow ring and sound to find an Echo
- Saved settings survive power cuts
- Temporary stock-Alexa-online mode for fetching models
- Draw your own light ring animations and theme packs on the settings page (beta)
- Windows (Git Bash) setup for an Echo that is already unlocked (beta)

**EchoLocal**

- 33 GPU audio visuals for screen saver and assistant states
- Wallpaper poster that changes hourly, daily or on each wake
- iBeacon advertising while the Bluetooth proxy is on
- Camera tuning: exposure, white balance, scene, effects, resolution
- YouTube cast with SponsorBlock skip and live delay
- Prime Video cast with Widevine provisioning
- Per-assistant screen visuals for listening, thinking and replying
- Visual ring and screen effects driven by room sound
- Host CLI encryption key show and rotate
- Host audio tools: capture analysis, direction estimate, test tones

**EchoMuse**

- Speaker protection: bass guard from stock values plus limiter
- Response/wake sound levels independent of volume; volume button beep
- Activity hardware trends kept 180 days, via API
- eMMC flash wear and boot reason reported per Echo
- emOS kernel crash detection from saved kernel logs
- Static controller address list for VLANs and tunnels
- Per-section fleet/device config scoping with revert
- Admin and read-only dashboard roles; admin-only speech data
- Ring pulses with speaker level; turn-end ring signals
- Speech gate stops music residue reaching speech-to-text

**Kiosk Satellite**

- Pauses dashboard rendering behind screensavers to cut CPU and heat
- Filters dashboard entity updates to the current view
- Dashboard links (ks://, app://) open kiosk features or apps
- URL Filter blocks or allows pages by pattern
- Home Launcher role with a crash fuse that hands it back
- GPS location sensors for HA
- Android broadcasts of kiosk events for Tasker and similar apps
- JavaScript API for dashboard and custom pages
- Custom update repository for kiosks without internet
- Audio quality pills (codec, bit depth, source) on Now Playing
- Celestial screensaver: live sky from bundled data, Point at the sky with a compass
- Music Library: browse and play from Music Assistant, Home Assistant or Sonos (2026.10.38)
- Home Assistant action that runs a shell command through Shizuku (2026.10.38)

**TECHO5**

- Spotify Connect receiver (Show and Dot, off by default)
- Talk through doorbell cameras over the ONVIF backchannel
- Reolink doorbell chime and camera pop-up without HA
- Now-playing sensors in HA for every source
- Presence turns the screen off when the room is empty
- Fixed IP address with trial and fallback (Show and Spot)
- Fonts of your own for CJK and other scripts
- Light-only and silent alarms; spoken alarm words
- Weather and radar centered on another HA zone
- Custom boot logo replacing Amazon's
- Volume down, volume up and next song words over music, no wake word
- Importable HA blueprints for voice commands and events (21 automation blueprints, 2 scripts)
- Screen skins (Pip-Boy, LCARS and more) and turn screens (Face, Rainbow, Orb)

**View Assist**

- Per-satellite views follow device attributes set by voice automations
- Asset update entities for dashboard, views and blueprints from GitHub
- Device Alerts blueprint: show icon, speak, change view on entity change
- Developer mimic device to preview a satellite's views on a PC
- Sports scores, Wikipedia search, Waze travel times, locate-a-person blueprints
- Rotating background images: local folder, Unsplash, URL or linked entity
- Custom listening overlays and status icon templates
- Spoken-time parser for timers in seven languages

## What each project focuses on

In each project's own terms.

- **echo-dot-assist**: It keeps Amazon's on-device audio front end and wake word engine while replacing Alexa with Home Assistant Assist, and firewalls the Echo off from Amazon's cloud. It aims to match stock Echo behavior locally (nearest-Echo answers, timers, Drop In, Music Assistant multiroom, Bluetooth), managed from a per-Echo settings page and Home Assistant.
- **EchoLocal**: "Turn an Amazon Echo Dot or Echo Show into a local Home Assistant voice satellite, speaker and display", as "a pure-Go replacement for Amazon's services" that Home Assistant adopts through the ESPHome native API. The optional integration covers what ESPHome can't express: wake word library, custom tones, turn recordings, groups and health.
- **EchoMuse**: It makes a cheap second-hand Echo Dot 2nd gen a Home Assistant voice satellite with "no Amazon account, no Alexa and no cloud service of ours", with the Echo listening for its wake word itself by default. Its stated direction is to stop depending on Amazon's software by moving devices to emOS, with setup, updates and rollback run from one controller dashboard.
- **Kiosk Satellite**: In its own terms, it is a dedicated Home Assistant kiosk app "purpose-built for Home Assistant from the ground up." It puts native Voice Satellite, synchronized music playback and photo screensavers first. It also stresses low CPU, GPU and heat use on older wall tablets, and deep Home Assistant control through a native ESPHome device, all fully local.
- **TECHO5**: TECHO5 turns old, unlockable Echo devices into private Home Assistant voice satellites with "Linux inside, Home Assistant in charge, no Amazon cloud", replacing Fire OS with Alpine Linux and one daemon that drives the hardware directly. Its own emphasis is on a useful touch screen (clock, calendar, dashboards, photos, cameras), alarms and house features that keep working when Home Assistant is down, and security by default with signed updates that "can't brick it".
- **View Assist**: View Assist focuses on adding visual feedback to Home Assistant Assist: per-device dashboard views driven by voice blueprints that users pick and choose, so Android tablets and voice-only satellites act as an Alexa Show / Nest Hub style network. VACA focuses on making setup simple with one Android app that provides on-device wake word, voice streaming, a media player and a browser display, with device controls and sensors exposed to Home Assistant.

## What the rows mean

<details>
<summary>Row definitions</summary>

**Basics**

- **What it is**: Fact: the form it takes, in a few words: replacement software on the device, an app installed on a stock OS, or a Home Assistant integration plus dashboard; name the parts if there are several
- **Started**: Fact: date of the first commit (per repo if there are several)
- **Releases**: Fact: latest release and its date; number of GitHub releases, with pre-releases counted separately
- **License**: Fact: the license of each main part, as the repo states it; "none stated" where a repo has no license file
- **Devices**: Fact: device models or device classes supported in a release; models in progress listed separately
- **What runs on the device**: Fact: the OS left on the device and the project's own program(s) running on it; say if a separate server is needed
- **Install**: Fact: what the user must do to install: unlock/root steps, USB or soldering, PC tool or script, APK sideload, HACS, and the host OS the tool needs
- **Replaces the device's OS**: Yes: a release installs its own Linux userspace or image so that no Android/Fire OS process runs on the device; Partly: that is one of several install paths or covers only some supported models
- **Works with other display or microphone apps**: Yes: documented setups where other projects' apps or satellites (browser apps, mic apps, ESPHome voice devices) act as its display or microphone; Partly: it hosts or interoperates with one other project's component
- **Interface languages**: Yes: the device or app interface ships in more than one language; Partly: only some screens (for example date and weather) are translated, or the catalog exists with English only
- **Porting to new hardware**: Yes: a documented path (guide, profile scripts or per-model files) for adding new device models; Partly: hardware notes or a protocol write-up only

**Voice**

- **Home Assistant Assist satellite**: Yes: the device appears in HA as an Assist satellite on its own; Partly: only through a separate app from another project
- **Follow-up listening**: Yes: listens again after a reply (HA continue_conversation) without the wake word; Partly: only after a question
- **HA can start a conversation**: Yes: supports HA's start_conversation and ask_question actions; Partly: only one of the two
- **On-device wake word**: Yes: the wake word is detected on the device itself; Partly: on-device detection only on some models or engines
- **Custom wake word models**: Yes: users can add their own wake word model files and use them on the device; Partly: own models only through HA's server-side engine
- **Two wake words, two assistants**: Yes: two wake words listened for at once, each starting its own pipeline; Partly: two words but one pipeline
- **Wake word on a server**: Yes: an option to stream idle mic audio to HA or a controller so the wake word is detected there; Partly: the server only double-checks uncertain on-device wakes
- **Start a turn without the wake word**: Yes: a button on the device, a screen control or an HA button starts listening; Partly: only a hardware button
- **Cancel a request in progress**: Yes: a button, the wake word or an HA button cancels while the assistant listens or thinks; Partly: cancel only by voice "stop" during the reply
- **Stop without the wake word**: Yes: saying "stop" (no wake word) ends a reply, timer or alarm; Partly: stopping needs the wake word first (barge-in) or works only for some sounds or engines
- **Nearest device answers**: Yes: several devices hearing the same wake word settle locally so only one answers; Partly: works only with a server or only within part of the fleet/engines
- **Echo cancellation**: Yes: the wake word and speech work over the device's own playback through echo cancellation; Partly: only for some sources or models
- **Noise suppression**: Yes: a noise suppressor cleans mic audio sent to the assistant; Partly: only for wake scoring, or only experimental
- **Mic gain and leveling**: Yes: adjustable mic gain or automatic leveling for speech sent to HA; Partly: fixed presets only
- **Mic array mixing**: Yes: combines several mics (averaging, beam choice or delay-and-sum) with a user choice of mode; Partly: fixed mixing only
- **External or USB microphone choice**: Yes: the user can pick an external USB or Bluetooth mic, or a channel of a mic array; Partly: a setup guide for external mics only
- **End of speech detected on device**: Yes: the device itself decides when speech ends (VAD), with adjustable timing; Partly: only a choice of HA's VAD sensitivity
- **Speech right after the wake word**: Yes: words said straight after the wake word, without waiting for a tone, reach HA; Partly: only with the wake sound turned off
- **Replies start while being generated**: Yes: speech starts before the full TTS reply is ready (streamed); Partly: streaming only for some agents or TTS engines
- **Saved voice recordings**: Yes: keeps recent request audio that can be played back or fetched; Partly: a short live replay buffer only
- **Voice-turn history**: Yes: a browsable log of past turns with transcript and reply; Partly: last-turn sensors only
- **Last-turn sensors in HA**: Yes: HA sensors for the last wake word or transcript and the reply; Partly: one of those
- **Wake-word clips and near misses**: Yes: saves clips of wakes, false wakes or near misses for tuning or training; Partly: counts near misses without audio
- **Uses the stock audio front end**: Yes: keeps the original vendor's mic processing and wake-word engine and feeds HA from it; Partly: a vendor engine is an option beside the project's own
- **Whisper detection**: Yes: detects whispered requests and passes that to HA/the agent so replies can match; Partly: documented as a template or recipe only
- **Realtime speech-to-speech AI**: Yes: the wake word can open a live speech-to-speech session with a cloud model (user's key or relay) that can control HA; Partly: realtime session without HA control
- **Assistant without Home Assistant**: Yes: speech-to-text, a chat model and text-to-speech can answer on the device with no HA pipeline; Partly: some on-device commands (keywords, local radio) work without HA but no general assistant
- **Library of voice-command add-ons**: Yes: ships a pick-and-choose set of voice-command packages (sentences plus responses/views) users install; Partly: one or a few example automations or blueprints for its own features
- **Music by voice**: Yes: a documented way to ask for an artist, album or song and have it play on the device (for example through Music Assistant); Partly: radio or a fixed stream only

**Audio and media**

- **HA media player**: Yes: an HA media player entity that plays TTS and play_media; Partly: TTS/announcements only
- **Music ducking**: Yes: music drops under voice replies or announcements and comes back; Partly: music stops instead of ducking
- **Sendspin / Music Assistant player**: Yes: a Sendspin player Music Assistant can group for synced multi-room; Partly: Music Assistant only through the HA media player
- **Bluetooth speaker for phones**: Yes: a phone can play to the device over Bluetooth A2DP; Partly: on some models only
- **Play on a Bluetooth speaker**: Yes: the device's audio can go out to a paired Bluetooth speaker or earbuds; Partly: one fixed speaker or codec limits stated
- **Headphone or line-out jack**: Yes: audio can go out of the device's 3.5 mm jack to another speaker; Partly: jack works but with stated limits
- **AirPlay receiver**: Yes: appears as an AirPlay target; Partly: on some models only
- **DLNA renderer**: Yes: appears as a DLNA/UPnP renderer that plays what is sent to it; Partly: audio only
- **Google Cast receiver**: Yes: acts as a Chromecast/Google Cast target for audio and video apps; Partly: audio only
- **Separate volumes per stream**: Yes: separate volume levels for at least music and assistant replies (often also alarms, calls); Partly: one offset, for example replies louder than music
- **Equalizer**: Yes: user EQ (bands or bass/treble) applied on the device; Partly: only an on/off speaker EQ switch
- **Speaker gain boost**: Yes: extra speaker gain beyond normal volume; Partly: only on some models
- **Own sounds and tones**: Yes: users can upload their own sounds for chimes, timers or alarms; Partly: a choice among built-in sounds only
- **Voice audio on another media player**: Yes: replies, chimes or announcements can be sent to a different HA media player; Partly: a separate music player is mapped but replies stay on the device
- **Internet radio**: Yes: a built-in radio player with station lists or favorites; Partly: radio only through a voice blueprint or another player
- **Sleep and ambient sounds**: Yes: built-in or generated sleep/ambient sounds (rain, white noise and similar); Partly: through a blueprint that plays streams
- **Sleep timer**: Yes: playback stops after a chosen time; Partly: only a duration option on one sound blueprint
- **Now playing screen**: Yes: an on-screen now-playing view with artwork and controls; Partly: a small card or strip only
- **Show and control another player**: Yes: the now-playing view can follow and control another HA, Music Assistant or Sonos player; Partly: shows another player without control
- **Synced lyrics**: Yes: shows lyrics in time with the music; Partly: plain lyrics only
- **Video playback on screen**: Yes: plays video sent from HA, DLNA or a cast app full screen; Partly: video only from one narrow source

**Screen**

- **Home Assistant dashboard on screen**: Yes: shows a chosen HA dashboard (web view, drawn or streamed) on the device; Partly: shows only its own tiles for HA entities or third-party tile pages
- **Other web pages on screen**: Yes: can show non-HA web pages full screen or as a screensaver; Partly: only through a voice command or view
- **Settings on the device screen**: Yes: most settings can be changed on the device's own screen; Partly: a few quick settings only
- **App drawer or launcher on screen**: Yes: a drawer, dock or launcher on the device screen opens its built-in apps or chosen Android apps; Partly: a fixed set of tabs only
- **Return to home view**: Yes: the screen goes back to a default view after inactivity; Partly: only back to the clock
- **Screen orientation**: Yes: orientation can be forced or follows a sensor, set from the app or HA; Partly: layout follows orientation but cannot be set
- **Keep screen on**: Yes: a switch keeps the display from timing out; Partly: only while a screensaver runs
- **UI scale**: Yes: the app's own interface (menus, text, keyboard) can be resized; Partly: web zoom only
- **Clock faces**: Yes: several built-in clock styles; Partly: one clock view
- **Weather display**: Yes: current weather and forecast on screen from an HA weather entity; Partly: current conditions only
- **Screensaver**: Yes: an idle screensaver with choices of what it shows; Partly: a dim or black screen only
- **Photo slideshow**: Yes: built-in photo slideshow from HA media, a device folder or a photo server; Partly: through a community view or tip only
- **Immich photos**: Yes: reads Immich directly with album or person filters; Partly: through a community tip only
- **Entity chips on the idle screen**: Yes: a row of HA entity states shown over the clock or screensaver; Partly: status icons on a dashboard only
- **HA notifications on screen**: Yes: HA can push a message/card (title and text) to the screen; Partly: only by switching to a full-screen alert view or by TTS with text
- **Timers on screen**: Yes: running timers count down on screen; Partly: only in a separate view
- **Calendar on screen**: Yes: shows HA calendars as an agenda or calendar view on the device; Partly: only through a voice command
- **To-do and shopping lists**: Yes: shows HA to-do lists and lets items be checked or added; Partly: view only
- **Answers shown as views**: Yes: the screen switches to an answer-specific view or panel (weather, info, list, camera) for voice replies; Partly: shows what was said and the reply as text only
- **Display modes**: Yes: named device modes (for example music, night, hold, cycle views) that HA or voice can switch; Partly: one or two such modes (for example hold or view rotation only)
- **Weather radar and alerts**: Yes: radar map and official weather alerts on screen; Partly: one of the two
- **Night mode**: Yes: the screen dims or changes to a night clock in a dark room or set hours; Partly: only a manual dark theme
- **Automatic brightness**: Yes: backlight follows a light sensor; Partly: only fixed levels set from HA
- **Themes**: Yes: selectable color themes for the device UI; Partly: light/dark only
- **Fonts**: Yes: a choice of screen fonts; Partly: font size only
- **Screen on/off and brightness from HA**: Yes: HA entities turn the screen on or off and set brightness; Partly: one of the two

**Cameras**

- **Show cameras on screen**: Yes: shows live feeds from HA or NVR cameras, including several at once; Partly: one camera at a time or snapshots only
- **go2rtc camera import**: Yes: reads camera streams from a go2rtc server directly; Partly: only through HA's camera entities
- **Camera sound**: Yes: plays a camera's own audio on the device; Partly: only for some protocols
- **Device camera as an HA camera**: Yes: the device's own camera is an HA camera entity; Partly: stills only on request
- **Device camera as RTSP/ONVIF**: Yes: the device's camera is offered as an RTSP stream found by ONVIF; Partly: RTSP or ONVIF with stated limits (for example not found by discovery)
- **Camera preview on the device**: Yes: an on-screen view of the device's own camera; Partly: a brief preview only for tuning detection
- **Camera presence, face and hand detection**: Yes: on-device camera detection of motion/presence plus face detection or hand/finger gestures; Partly: motion/presence only

**Communication**

- **HA announcements**: Yes: HA announcements (TTS or audio) play on the device; Partly: only as plain media playback
- **Intercom between devices**: Yes: two-way calls from one device to another on the LAN; Partly: only between some models
- **Broadcast to every device**: Yes: one voice or text message goes to all devices at once; Partly: only through HA scripts the user writes
- **Auto-answer (Drop In)**: Yes: an intercom call can connect without a tap, after a chime or countdown; Partly: only from chosen devices
- **Call by voice**: Yes: "call the kitchen" style requests start a call, through a shipped blueprint or built-in; Partly: an example automation the user adapts
- **Video calls between devices**: Yes: device-to-device calls with video; Partly: video on some models only, the rest audio
- **Phone calls**: Yes: real phone calls (for example through a SIP provider) in and out; Partly: outgoing only or by recipe only

**Sensors and controls**

- **Hardware buttons as HA events**: Yes: the device's buttons fire HA events (press, hold or multi-tap); Partly: some buttons only
- **Mic mute reported to HA**: Yes: mic mute state is an HA entity; Partly: mute works but is not reported
- **Light sensor in HA**: Yes: a lux sensor entity in HA; Partly: light sensor used on the device only
- **Bluetooth proxy**: Yes: ESPHome-style BLE proxy with active connections; Partly: passive scanning only
- **LED ring control from HA**: Yes: HA can drive the light ring as a light or with effects; Partly: brightness only
- **LED ring styles**: Yes: selectable ring styles or themes for listening, thinking and other states; Partly: fixed stock-like states only
- **Touch gestures**: Yes: touch gestures that can run actions or send HA events; Partly: fixed gestures for built-in navigation only
- **Clap detection**: Yes: claps detected and usable as a trigger; Partly: experimental only
- **Proximity sensor**: Yes: the device's proximity sensor wakes the screen or is an HA event; Partly: used on the device only
- **Room loudness sensor**: Yes: room sound level as an HA sensor (number only); Partly: used on the device only
- **Camera shutter state**: Yes: the lens cover or shutter state is detected and reported; Partly: detected but not reported
- **Do not disturb**: Yes: a DND switch that silences announcements or refuses calls; Partly: DND for one feature only
- **Device health readings**: Yes: CPU, temperature, memory or Wi-Fi readings shown to the user (HA sensors or a status page); Partly: one or two readings only
- **Sound event detection**: Yes: on-device detection of sounds such as smoke alarms or glass breaking, sent to HA; Partly: only a loudness level
- **Wi-Fi motion sensing**: Yes: a motion sensor derived from Wi-Fi signal changes (experimental or off-by-default still counts); Partly: not used for this row

**Home Assistant**

- **ESPHome native API link**: Yes: HA adopts it through the ESPHome integration over an encrypted API; Partly: ESPHome API without encryption, or a custom HA integration instead
- **Settings as HA entities**: Yes: most settings are HA switches, selects and numbers; Partly: a few settings only
- **Custom HA actions**: Yes: the device offers its own HA actions for automations (show, play, call, set and similar); Partly: a few actions only
- **Events on the HA bus**: Yes: the device fires HA events for its activity (voice steps, calls, alarms, gestures); Partly: events for one feature only
- **Change the view from HA**: Yes: HA can send the device to a chosen dashboard view or page; Partly: only open a URL over the screen
- **Launch Android apps**: Yes: HA or dashboard links can open installed Android apps; Partly: through a launcher list only
- **Time and time zone from HA**: Yes: the device clock and time zone are set from HA; Partly: time zone only
- **Voice timers**: Yes: HA voice timers ring on the device and can be stopped there; Partly: timers ring only through a media player
- **Alarms**: Yes: alarms with repeat days and snooze that ring on the device; Partly: alarms only through HA scripts or a single alarm switch
- **Reminders**: Yes: one-time or repeating reminders spoken or shown on the device; Partly: through a blueprint only
- **Sunrise wake light**: Yes: the screen or ring brightens before an alarm; Partly: room lights only
- **Keeps working when HA is down**: Yes: alarms, calls or media keep working on the device with HA offline, as documented; Partly: only playback that keeps running for a short time
- **Control a group of devices**: Yes: one control in HA mutes, rings or stops every device in a chosen group; Partly: a ready-made stop-all script or similar fixed recipe

**Management**

- **Over-the-air updates**: Yes: updates offered and installed from HA or the device; Partly: only by pushing a build from a PC
- **Update channels**: Yes: a choice of stable and beta/dev (or early access) updates; Partly: channel set by which add-on or build is installed
- **Rollback after a failed update**: Yes: a failed update falls back to the last good version on its own; Partly: manual rollback only
- **Signed updates**: Yes: only signed update images install; Partly: images checked by hash only
- **Web settings page**: Yes: a web page served by the device or its controller to change settings; Partly: a page for setup or onboarding only
- **Log viewer**: Yes: device logs readable in a browser or HA; Partly: logs only by command line
- **Remote screenshot**: Yes: a screenshot of the device screen over HTTP, an API or as an HA camera; Partly: only by an on-device command
- **Diagnostics download**: Yes: a downloadable diagnostics/support file with secrets removed; Partly: raw logs only
- **Remote shell**: Yes: a root or app shell over the network (SSH, adb, console); Partly: USB or on-device only
- **Remote API**: Yes: an authenticated HTTP/WebSocket API to read and change settings; Partly: read-only or unauthenticated
- **Copy settings between devices**: Yes: settings can be copied or synced from one device to others; Partly: defaults from one central config only
- **Settings export and import**: Yes: settings can be saved to a file and restored; Partly: export without restore
- **Change Wi-Fi without reinstalling**: Yes: switch networks from the device, its page or its screen; Partly: only with a PC tool over USB
- **Restart or reboot remotely**: Yes: restart the software or reboot the device from HA or a web page; Partly: only from the device itself
- **Automatic recovery after a crash**: Yes: the app or page reloads or restarts by itself after a crash or load failure; Partly: only a manual reload button
- **Recovery and way back to stock**: Yes: a documented rescue mode and a way back to the stock OS; Partly: one of the two
- **List of other devices**: Yes: the device's page lists the other devices it found on the network and lets you jump to them; Partly: a list without links
- **Device naming**: Yes: the device name/ID can be changed after setup; Partly: only before pairing, or a fixed generated name
- **Reset or leave HA**: Yes: a factory reset or unpair action that clears the HA link for handing the device on; Partly: only by reinstalling
- **Central management server**: Yes: a separate server or add-on that holds config and updates for every device; Partly: management from an HA integration or from a leader device
- **Browser-based installer**: Yes: the install runs from a web browser (for example over WebUSB) with no local tools; Partly: a guided one-command installer or script on a PC

**Privacy and security**

- **Vendor cloud services removed**: Yes: the original vendor's assistant and cloud services are removed or blocked; n/a for apps on a stock OS; Partly: only some services disabled
- **Inbound firewall**: Yes: a firewall limits inbound traffic to the ports the device serves; Partly: only some services closed by default
- **Signed or encrypted device-to-device traffic**: Yes: traffic between devices (arbitration, intercom, sync) is keyed or signed; Partly: only some of it
- **Login for settings and admin**: Yes: settings page or API needs a login, button approval or PIN, with rate limits on failures; Partly: login without rate limits or only for some functions
- **Sendspin pairing**: Yes: only paired Music Assistant servers can play, with play-without-pairing opt-in; Partly: pairing optional and off by default
- **HTTPS and certificate options**: Yes: verifies HTTPS certificates and lets the user trust a private CA or install its own certificate; Partly: only a switch to turn checks off, or an accept-once prompt
- **Mic and camera indicators**: Yes: on-screen marks while mic or camera is in use, and camera off with mute; Partly: one of those
- **No telemetry by default**: Yes: no analytics or crash reporting is sent by default; Partly: analytics present but off by default or only in some builds
- **Kiosk lockdown**: Yes: locks the device to the app (exit PIN, system UI and button blocking, relaunch); Partly: a settings PIN or launcher role only

**Extensibility**

- **User-made screen pages**: Yes: users can add their own pages, views or HTML as screens; Partly: only custom styling or templates
- **Plugin system**: Yes: third-party plugins can be installed and add features, with a published SDK; Partly: user-written views, scripts or custom code without a plugin API
- **Wake word training**: Yes: ships a tool that trains wake word models; Partly: a written guide to training with outside tools
- **Stream deck and computer control**: Yes: the screen works as a button deck that drives OBS or a computer; Partly: buttons that call HA only

</details>

## Sources

- echo-dot-assist: [Gamer92000/echo-dot-assist](https://github.com/Gamer92000/echo-dot-assist)
- EchoLocal: [ygelfand/echolocal](https://github.com/ygelfand/echolocal), [echolocal-hacs](https://github.com/ygelfand/echolocal-hacs)
- EchoMuse: [wilbowes/EchoMuse](https://github.com/wilbowes/EchoMuse)
- Kiosk Satellite: [jxlarrea/kiosk-satellite](https://github.com/jxlarrea/kiosk-satellite), [kiosk-satellite-plugins](https://github.com/jxlarrea/kiosk-satellite-plugins)
- TECHO5: [HuskerMinion/techo5](https://github.com/HuskerMinion/techo5), [techo5-dot](https://github.com/HuskerMinion/techo5-dot), [techo5-spot](https://github.com/HuskerMinion/techo5-spot), [techo5-panel](https://github.com/HuskerMinion/techo5-panel)
- View Assist: [dinki/View-Assist](https://github.com/dinki/View-Assist), [view_assist_integration](https://github.com/dinki/view_assist_integration), [ViewAssist_Companion_App](https://github.com/msp1974/ViewAssist_Companion_App)
