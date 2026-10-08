# TECHO5 Deck

TECHO5 Deck turns an Echo Show into a stream deck: pages of touch buttons that switch OBS scenes,
start and stop the stream and the recording, mute your microphone and show or hide sources, and
that press shortcuts, type, open apps and websites and run scripts on your computer. The buttons
talk to OBS and the computer directly, so they work without Home Assistant, and they show what OBS
is doing: the live scene lit, the stream button lit while you're live, a muted input crossed out.

Show only (the Echo Show 5 and Show 8). OBS 28 and later has what the OBS buttons need built in;
the computer buttons need the small TECHO5 Deck agent on the computer
([below](#computer-buttons)).

## Set up OBS

In OBS, open **Tools → WebSocket Server Settings**:

1. Turn on **Enable WebSocket server**.
2. Leave **Enable Authentication** on, and note the password (**Show Connect Info** shows it).
3. Note the port: 4455 unless you changed it.

Your computer's firewall has to let the Show reach that port. Windows asks the first time OBS
listens; answer **Allow** for private networks.

## Set up the deck

On the device's setup page, **Screen & Photos → Deck**:

1. **OBS address**: your computer's address, like `192.168.1.20` (add `:4456` if you changed the
   port). Then the password, and **Save**. The line under it says **Connected** once OBS answers.
2. **Buttons across** and **Rows**: 4 by 3 to start with; up to 6 by 4.
3. **Page 1**: for each button, an **action** and what it acts on:
   - **Switch scene**: the scene, in **Scene or input**. The box suggests OBS's own scene names
     while it's connected.
   - **Stream on/off** and **Record on/off**: nothing more.
   - **Mute/unmute**: the input (your microphone, Desktop Audio) in **Scene or input**.
   - **Show/hide a source**: the scene in **Scene or input**, and the source in **Source**.

   A **label**, a **color** and an **icon** are optional; each action has its own. Icons are
   [Material Design icon](https://pictogrammers.com/library/mdi/) names, like `microphone-off`.
   **Save page**, and **Add a page** for more.

## Use it

- **Open it** with a swipe up from the bottom edge of the clock: start right at the bottom of the
  screen. Once a deck has buttons, a swipe up that starts there opens it; anywhere else, and on the
  now-playing and weather pages, a swipe up is still the volume. You can also set **Tap on the clock** to
  **Deck** (setup page, Screen & Photos, or Settings → Display on the device).
- **Press** a button. It lights up and sinks while the press goes out, and turns red for a moment if
  it failed.
- **Swipe left and right** for the pages, and **down** to put the deck away. It stays up until you
  do.
- When OBS isn't running, the buttons gray out and the foot of the deck says why.

Home Assistant can press a button too, with the `deck_press` action
([Actions](actions.md#press-a-deck-button)).

## Computer buttons

Buttons can press a shortcut or a media key, type text, open an app or a website, or run a script
on a computer. That needs the **TECHO5 Deck agent** running there: one program, nothing to install,
for Windows, macOS and Linux.

1. Download the agent for your computer from the
   [latest release](https://github.com/HuskerMinion/techo5/releases/latest):

   | Computer | File |
   |---|---|
   | Windows (most PCs) | `techo5-deck-windows-amd64.exe` |
   | Windows on ARM | `techo5-deck-windows-arm64.exe` |
   | Mac with Apple silicon (M1 and later) | `techo5-deck-macos-arm64` |
   | Mac with Intel | `techo5-deck-macos-amd64` |
   | Linux (most PCs) | `techo5-deck-linux-amd64` |
   | Linux on ARM (a Raspberry Pi 4 or 5 with 64-bit Linux) | `techo5-deck-linux-arm64` |

   On Windows, run it. On macOS and Linux, run it from a terminal (see [macOS](#macos) and
   [Linux](#linux) for the one-time steps each needs). It shows the computer's name and a **pairing
   key** of 32 letters and digits. The first time, Windows asks whether to let it on the network:
   allow it on **private networks**.

   Each file is built by GitHub from the release's own source; check one with
   `gh attestation verify <file> --repo HuskerMinion/techo5`.
2. On the Show's setup page, **Screen & Photos → Deck → Computers**: **Look for computers**, pick
   yours (or type its address), type the key, and **Pair**. It says **Connected** with how many
   apps and scripts it found.
3. On a page, give a button a **Computer** action and pick the computer:
   - **Press keys**: a shortcut the way you'd write it, `ctrl+shift+m`, `alt+f4`, `win+d`, `f13`,
     or a media key: `media_play_pause`, `media_next`, `media_previous`, `volume_up`,
     `volume_down`, `volume_mute`.
   - **Type text**: the words to type, as they are.
   - **Open app or website**: an app by its name, as the Start menu, the Applications folder or
     the desktop's app list names it (the box suggests them), or an `https://` address.
   - **Run script**: a name from the agent's script list (below).

Keep the agent's window open while you use the deck. To have it start when you sign in, run it
once with `-startup on`, plus any other options you use, like `-name` (`-startup off` undoes it).

### macOS

A downloaded file isn't marked as a program, and macOS holds back programs from the internet that
aren't from the App Store or a known developer. In Terminal, in the folder you downloaded it to:

```
chmod +x techo5-deck-macos-arm64
xattr -d com.apple.quarantine techo5-deck-macos-arm64
./techo5-deck-macos-arm64
```

(`macos-amd64` on an Intel Mac.)

The agent presses keys through macOS's own automation, which needs two permissions once:

1. When the agent starts, macOS asks whether Terminal (or the agent) may control **System
   Events**: choose **Allow**.
2. In **System Settings → Privacy & Security → Accessibility**, turn on Terminal (or the agent).
   It appears in that list after the first key press is refused. Then start the agent again.

Until then the agent's window says what's missing, and the setup page says it can't press keys
there yet. macOS also asks the first time the agent goes on the
network: allow it. All keys work except Print Screen and F21 to F24, which Mac keyboards don't
have; `cmd` is the Command key.

### Linux

Mark the download as a program and run it: `chmod +x techo5-deck-linux-amd64`, then
`./techo5-deck-linux-amd64`.

The agent makes a virtual keyboard, which works the same under X11 and Wayland. That needs
permission to use `/dev/uinput` once:

```
sudo groupadd -f --system uinput
sudo usermod -aG uinput $USER
echo 'KERNEL=="uinput", GROUP="uinput", MODE="0660"' | sudo tee /etc/udev/rules.d/60-techo5-deck.rules
sudo udevadm control --reload && sudo udevadm trigger
```

Then sign out and back in. **Type text** types plain letters, digits and punctuation, as a US
keyboard layout has them; other characters aren't typed yet. Apps are the ones in your desktop's
app list; they open with `gtk-launch` or `gio`.

### The script list

Scripts are listed on the computer, never on the Show: a paired Show can run only what's in the
list, and only somebody at the computer can change it. The list is `scripts.txt` in the agent's
folder (`%APPDATA%\TECHO5 Deck` on Windows, `~/Library/Application Support/TECHO5 Deck` on macOS,
`~/.config/TECHO5 Deck` on Linux), one per line, a name, `=`, and the command:

```
Backup = robocopy "C:\Users\me\Documents" "D:\Backup\Documents" /MIR
Lights = "C:\Tools\lights.exe" --scene evening
```

Save it, then **Refresh** on the setup page (the Show also asks again every minute).

### Good to know about the agent

- **A paired Show has the run of your signed-in session.** A deck that can press keys and type can
  open a terminal and type a command into it, so the script list is what Run script buttons pick
  from, not a fence. Pair only Shows you trust, and set the Show's **settings lock** (Settings →
  Privacy & Security) so nobody can change its buttons from its screen. Its setup page already
  needs a press on the Show to let a browser in, and Home Assistant can press deck buttons too
  (`deck_press`). The deck itself stays usable while the settings are locked: it's a control
  surface, like the clock's other pages.
- It takes connections from your local network only, and only from a Show that has its key: the
  connection is encrypted with that key, and a wrong key gets nothing.
- Run it with `-new-key` to make a new key; every paired Show then has to pair again.
- **Type text** buttons never show their text on the Show (they say "Type text" unless you give
  them a label), and the agent's window logs only how many characters it typed.
- Windows won't let it press keys into a window that's running as administrator, or while the
  screen is locked. A key press turns the button red and the agent's window says why; **Type text**
  answers the Show before it types (a long text takes a while), so a failure partway shows only in
  the agent's window.
- Scripts run with the shell of the computer: `cmd` on Windows, `sh` on macOS and Linux.

## Good to know

- OBS's WebSocket isn't encrypted. The password itself is never sent (OBS uses a challenge), but
  scene names and commands are readable on your network. That's fine at home; don't point the deck
  at an OBS across the internet.
- The password is kept on the device. The setup page only says whether one is saved.
- The agent's key and the OBS password are kept on the Show; the setup page only says whether
  they're set.
