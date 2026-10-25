//go:build !panel

package home

// cameraStreamHere is whether a camera's own stream can be played: live video and its sound, decoded
// by the image's techo5-ffmpeg (camera_stream.go, camera_sound.go).
const cameraStreamHere = true
