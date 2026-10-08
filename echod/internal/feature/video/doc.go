// Package video plays a video full screen on the Show: an address from Home Assistant's play_video
// action, or from a DLNA controller while DLNA video is on (docs/video.md, docs/cast-plan.md).
//
// The daemon does not decode video itself. ffmpeg does, as a child process: the image carries a small
// build of it (tools/linux/build-ffmpeg.sh) that knows the network and the common formats and nothing
// else, run as an unprivileged user under resource limits, allowed only the network protocols, and
// killed when the video ends. It hands back two pipes: the picture as raw frames already scaled,
// letterboxed and turned for the panel, and the sound as 48 kHz stereo. The sound plays through the
// media player as a received track, so the volume, a turn's ducking and "whatever starts next takes
// the speaker" work as they do for music; the picture follows the sound, each frame shown when the
// sound reaches it and dropped when a later one is already due. The display owns the screen and
// paints the frames (feature/display, hardware/screen).
//
// The Show and the Spot (whose round face has a layout of its own, in feature/display). The Dot has no
// screen: there the package says it is not here, and DLNA offers no video.
package video
