//go:build !dot && !spot

package display

import (
	"fmt"

	fcamstream "github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/facedetect"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/jpegenc"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/vencpipe"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The ports the camera is served on, for the camera test to name: the stream's, and the web port, which
// has the pages and ONVIF.
const (
	camTestRTSPPort = fcamstream.Port
	camTestWebPort  = web.Port
)

// camTestPath is how this board makes its camera's pictures, as the camera test says it: the sensor,
// what develops its frames for the stream, and what makes the pages' JPEGs, a paragraph each. It is
// read off the blocks that are actually there (camstream, vencpipe, facedetect, jpegenc), not the board
// alone, so a board without its encoder says so.
func camTestPath() []string {
	st := fcamstream.Stream()
	stream := ""
	if st.Codec == "H264" {
		stream = fmt.Sprintf(" for RTSP %dx%d at %d fps, %.0f Mbit/s", st.W, st.H, st.FPS, float64(st.Bitrate)/1e6)
	}
	jpeg := "Pages: JPEGs developed and encoded on the cores"
	var path []string
	switch {
	case layout.AthenaMainline():
		sensor := "OV13855, RAW10 2112x1568, 60 fps (down to 15 only in the dark)"
		if camstream.Available() {
			path = append(path, sensor+" → ISP pass 2 (mtk-dip, MDP3) → NV12 → H.264 encoder (mtk-vcodec-enc)"+stream)
		} else {
			path = append(path, sensor+" → developed on the cores; no ISP or encoder here, so no stream")
		}
		if facedetect.Available() {
			path = append(path, "Faces: the face detector (mtk-fd), through the SCP proxy")
		}
		if jpegenc.Available() {
			jpeg = "Pages: developed on the GPU where it answers, encoded on the JPEG encoder (mtk-jpeg-enc)"
		}
	case layout.Board == "cronos":
		sensor := "OV02B10, RAW10 1600x1200, 30 fps (down to 9 only in the dark) → developed on the cores to I420 while the encoder takes the frame before"
		if camera.ISPDevelops() {
			sensor = "OV02B10, RAW10 1600x1200 → developed on the ISP's pass 2 (cronos-camera, in the sensor's blanking: 25.7 fps at most; the cores if it fails) to I420 while the encoder takes the frame before"
		}
		if vencpipe.Available() {
			path = append(path, sensor+" → MT8163 H.264 encoder (MediaTek's libraries on /dev/Vcodec)"+stream)
		} else {
			path = append(path, sensor+"; MediaTek's encoder libraries are missing, so no stream")
		}
	case layout.Checkers() || layout.Crown():
		path = append(path, "OV9734, RAW10 1280x720 → developed on the cores"+stream)
	default:
		path = append(path, "Sensor → developed on the cores"+stream)
	}
	return append(path, jpeg)
}
