//go:build dot

package security

// webPages: the Echo Dot has no camera and no screen, so their web access switches are not offered.
const (
	webPages   = false
	cameraPage = false
)
