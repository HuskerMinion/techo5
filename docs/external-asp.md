# External microphone ASP

TECHO5 has an optional interface for processing captured microphone audio with an external helper. The
normal Built-in audio pipeline remains the default and requires no additional software.

This document describes the interface implemented by TECHO5, not how to install a particular helper. For
the Home Assistant controls, see [Setting it up](setup.md#external-microphone-processing).

## Purpose and scope

External ASP lets a compatible helper process native microphone and playback-reference PCM before TECHO5
selects the mono audio sent to wake word detection, speech recognition, history and other microphone
listeners.

Responsibilities are separate:

- **TECHO5** owns capture, the native PCM descriptor, interprocess communication (IPC), helper launch and
  lifecycle, fallback, the SoftwareCut privacy boundary and downstream audio delivery.
- **The external helper** owns the audio processing algorithm. It must accept the descriptor and PCM
  supplied by TECHO5 and return the required mono output.
- **[FrankenASP-v3](https://github.com/dmit2k/frankenasp-v3)** is an independent project that uses this
  interface for factory audio processing experiments. It is neither part of TECHO5 nor required to use
  the interface.

TECHO5 does not download, install or configure an external helper when External is selected. The
interface does not prescribe a particular processing algorithm or vendor implementation.

**External ASP is strictly opt-in.** With Built-in selected, no external helper is launched, no External
ASP IPC or native PCM exchange takes place, and no additional external audio processing is performed. The
normal TECHO5 capture, processing, sensor and downstream audio behavior remains unchanged. Only
lightweight mode checks and the Home Assistant control entities remain; no external helper CPU, memory or
IPC workload is incurred.

## Parallel built-in processing and output selection

Selecting External does not disable or bypass TECHO5's built-in audio processing pipeline.

**Both processing paths continue operating while External is active.** Native captured PCM is supplied to
the external helper, while TECHO5 continues its normal microphone mixing, echo cancellation, noise
reduction, leveling and associated measurements.

This is intentional. Several TECHO5 features depend on audio samples or measurements taken at different
stages of the built-in pipeline, rather than on the final mono stream delivered to wake word detection
and speech recognition.

These include:

- **Ambient noise sensor:** measures the room's sound level using the built-in signal before noise
  reduction and leveling, together with the captured playback reference.
- **Room sensitivity and speech standout:** estimate how far sounds stand out above the room's background
  level. The measurements also support nearest-device voice selection.
- **Sound direction detection:** uses captured microphone channels and TECHO5's own beamforming
  calculations to estimate the direction of sound, including for the light ring on supported devices.
- **Double-clap detection:** independently consumes raw microphone PCM and playback-reference channels.
  It does not depend on the selected mono ASP output.
- **Built-in DSP state:** microphone mixing, echo cancellation, noise reduction and leveling continue to
  process captured frames and maintain their normal internal state.

These operations are not switched to the external helper's output. They continue using their existing raw
capture inputs or intermediate built-in processing stages.

The two processing paths meet at a single final output-selection point:

1. TECHO5 processes the captured frame through its normal built-in pipeline and produces a Built-in mono
   frame.
2. When a compatible external session is running, the native captured buffers are also sent to the
   external helper, which returns an External mono frame.
3. TECHO5 selects one of these frames for downstream publication.
4. The selected frame is used consistently by microphone history, microphone spectrum, wake word
   detection, speech recognition and other mono audio listeners.

The built-in result is computed even when External is selected and successfully supplying the downstream
stream. External does not replace the internal measurements or disable the raw PCM consumers.

**This also provides immediate failure fallback.** Because Built-in processing continues throughout an
External session, its output and processing state are already available if the helper fails, is canceled
or produces an invalid response. TECHO5 can continue with the Built-in frame without reopening capture,
restarting the internal DSP or reconstructing its processing state.

This distinction is fundamental to the interface: **External selects the downstream mono audio source; it
does not replace the entire TECHO5 audio subsystem.**

## PCM descriptor v1

TECHO5 sends a PCM-only descriptor once when starting a helper session. The descriptor describes physical
capture buffers and their channels, not a processed or resampled microphone mix.

All multibyte descriptor fields use little-endian encoding.

The descriptor begins with a 16-byte header:

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | Magic: `ASP1` |
| 4 | 2 | Version: `1` |
| 6 | 1 | Buffer count |
| 7 | 1 | Channel count |
| 8 | 4 | Total descriptor size in bytes |
| 12 | 4 | Total input PCM size in bytes |

The header is followed by one 28-byte record per buffer and then one 8-byte record per channel. The
descriptor size is exactly `16 + 28 * buffer_count + 8 * channel_count`.

Each buffer record contains:

| Field | Size | Meaning |
|---|---|---|
| `offset` | 4 | Byte offset in the concatenated input block |
| `length` | 4 | Buffer length in bytes |
| `sample_rate` | 4 | Samples per second |
| `frames` | 4 | Number of capture frames |
| `stride` | 4 | Bytes between successive frames |
| `format` | 1 | PCM storage format |
| `valid_bits` | 1 | Significant sample bits |
| `lsb_shift` | 1 | Position of the significant bits within the storage word |
| `channels` | 1 | Channels described by this buffer |
| `first_channel` | 2 | First channel record belonging to this buffer |
| Reserved | 2 | Zero in descriptor v1 |

Supported format codes are `1` for signed S16LE, `2` for signed packed S24_3LE and `3` for signed S32LE.
Storage widths are respectively 2, 3 and 4 bytes.

`valid_bits` and `lsb_shift` describe the captured sample representation. For example, 24 valid bits in
S32LE may be stored with an LSB shift of 0 or 8. Their sum must not exceed the storage width in bits. The
descriptor does not request a PCM conversion.

Each channel record contains:

| Field | Size | Meaning |
|---|---|---|
| `buffer_index` | 1 | Zero-based index of the containing buffer |
| `role` | 1 | `1` = MIC, `2` = REF |
| `role_index` | 2 | Zero-based channel number within its role |
| `frame_offset` | 4 | Byte offset of the channel's sample within each frame |

The channel's sample in capture frame `n` begins at `n * stride + frame_offset` within its buffer.

Buffers are ordered as captured. Their offsets are consecutive, starting at zero; their channel records
occupy consecutive ranges defined by `first_channel` and `channels`. MIC and REF role indices are
separately consecutive and start at zero. At least one MIC channel is required; REF channels are
optional.

Each declared sample must fit within its frame and buffer, including the final frame. Samples in the same
buffer must not overlap. The maximum descriptor contains 255 buffer records and 255 channel records.

The native capture interface can expose one interleaved buffer or multiple physical buffers. MIC and REF
may have different formats, sample rates or frame geometry when supplied as separate buffers. TECHO5 does
not invent missing REF channels or advertise unknown capture layouts as a supported format.

## IPC and frame exchange

The helper runs as a separate process. TECHO5 communicates through the helper's standard input and
standard output.

Startup proceeds as follows:

1. TECHO5 validates and launches the helper.
2. TECHO5 writes the complete descriptor v1 to standard input.
3. The helper validates the descriptor and writes the single READY byte `0xA5` to standard output.
4. After READY, TECHO5 can begin PCM frame exchanges.

For each exchange, TECHO5 writes the complete native PCM buffers to standard input, in descriptor order,
without a separate per-frame header or length prefix. Each buffer must have the length specified by its
descriptor record.

For each input exchange, the helper must write exactly 640 bytes of mono signed S16LE PCM to standard
output: 320 samples at 16 kHz, representing 20 milliseconds of audio.

The helper is responsible for any channel combination, reference handling, resampling, echo cancellation
or other processing needed to produce that output.

Native input buffers are borrowed from capture. Processing must finish before the next capture read; the
helper cannot rely on their contents being retained by TECHO5. The returned output has independent
storage.

## Secure launcher

TECHO5 looks for the helper executable at this fixed path:

`/data/techo5-linux/external-asp/bin/external-asp`

This is an installation contract, not a configurable setting.

The launcher walks the path through directory file descriptors, using `openat` with `O_NOFOLLOW` to
reject symbolic links at the traversed components.

The `/data` and `/data/techo5-linux` ancestors are traversal paths, not trusted ownership or permissions
boundaries.

The trusted `external-asp` and `external-asp/bin` directories must be root-owned and have mode `0700`.
The executable must be a root-owned regular ELF file, readable and executable by its owner, without group
or other write permissions or special permission bits.

TECHO5 verifies the opened objects and retains their file descriptors so that execution uses the verified
objects rather than reopening their paths.

The child receives:

- **FD3:** the pinned executable, invoked through `/proc/self/fd/3`.
- **FD4:** the pinned `external-asp` assets directory.
- **stdin:** native PCM descriptor and input frames.
- **stdout:** READY and processed PCM frames.
- **stderr:** the daemon's standard error stream.

The helper runs with a restricted environment containing `LANG=C` and `LC_ALL=C`.

A helper installation must satisfy this contract. TECHO5 itself does not provide an installer for
external helpers.

## Selection, lifecycle and fallback

The Home Assistant configuration select **Microphone ASP selected** has two values: Built-in and
External. Its default is Built-in. The selection is persisted independently of whether the helper starts
successfully.

The diagnostic text sensor **Microphone ASP active** reports the processor currently supplying the
downstream mono stream.

With Built-in selected, the external processing path is dormant and adds no helper processing workload.
An Active value of Built-in alone does not imply that the external launch path is idle: External may
already be selected while a helper is still starting.

Selecting External requests an asynchronous launch. The Built-in processing path continues while the
helper starts and waits for READY. The startup deadline is 10 seconds and covers secure launch,
descriptor delivery and READY. Once startup succeeds, that deadline no longer applies to the resident
session.

READY alone does not activate External. The first successful PCM exchange must also produce a valid
output frame that TECHO5 accepts for downstream publication. Until then, Active remains Built-in.

Each PCM exchange has a 5-second timeout covering the complete operation, including input writes and
output reading.

Built-in processing continues even after Active changes to External. This preserves the internal audio
measurements and processing state described in
[Parallel built-in processing and output selection](#parallel-built-in-processing-and-output-selection).

A launch failure, invalid native geometry, changed geometry, failed PCM exchange or invalid output causes
fallback to Built-in. The saved External selection is not overwritten. Since the Built-in path has
continued processing every frame, fallback does not require rebuilding or restarting it.

TECHO5 does not automatically launch another helper after an attempt fails. While the daemon continues
running, a manual retry requires changing the selection from External to Built-in and then back to
External.

The session belongs to the resident `echod` lifetime, not to an individual capture-open interval. Closing
and reopening capture does not by itself terminate the helper. A session may be reused when the entire
buffer and channel geometry remains unchanged.

If a new capture block has different geometry, TECHO5 does not send it under the old descriptor. The
External attempt fails and Built-in processing continues.

Changing to Built-in, replacing an attempt or shutting down the daemon invalidates the old attempt and
cancels its operations. IPC pipes are closed before cleanup waits for process termination. The session
owns the child process and its single process wait; no separate helper supervisor or automatic reconnect
mechanism is provided.

## SoftwareCut privacy boundary

SoftwareCut is enforced at the capture input boundary, before downstream processing and resampling.

The contract is:

1. MIC samples crossing that boundary while software mute is active are zeroed.
2. PCM already accepted before mute activation may finish an in-flight helper exchange and reach
   downstream listeners or audio history after mute becomes active.
3. No second mute check at the output, output muting or retroactive discard of in-flight PCM is required.
4. REF channels are not required to remain silent by this interface during mute; REF may continue to be
   supplied. A capture implementation may also zero REF.
5. The same capture-boundary policy applies whether Built-in or External processing is selected.

SoftwareCut is a software capture policy on platforms that use it. It does not replace a platform's
hardware microphone mute.

## Limitations

External ASP is an optional interface, not a bundled audio processing implementation. Its presence in
TECHO5 does not establish that a compatible helper exists for a given device.

Native capture geometry depends on the capture backend. Backends that cannot provide a representable
native PCM descriptor continue with Built-in processing.

A helper must support the actual MIC/REF layout, sample formats, rates and buffer ordering of the
platform on which it runs. A helper compatible with one Echo model is not automatically compatible with
another.

The interface should not be taken as a claim of hardware-tested External ASP support across every TECHO5
device. Hardware compatibility and testing must be established for each particular helper and platform.

The standard TECHO5 configuration remains independent of External ASP and its associated projects.

## Add-ons others have made

These are made and kept by their authors, not by TECHO5, and TECHO5 does not test them. Read each one's own
instructions and notes before you install it.

- [techo5-sound-ai](https://github.com/calin2k/techo5-sound-ai) by calin2k: an on-device room sound classifier
  (Google's YAMNet) with an activity alarm, run from `/data` through `rc.local` and this interface, so it
  outlasts updates. Its sound classes are for information only; it is not a life-safety device.
