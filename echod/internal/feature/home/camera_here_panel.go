//go:build panel

package home

// cameraStreamHere is false on a panel: the app carries no techo5-ffmpeg, so a camera is shown from
// Home Assistant's snapshots and the live video and camera sound switches are left out.
const cameraStreamHere = false
