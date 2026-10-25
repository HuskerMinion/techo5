//go:build panel

package ambient

// Here: a panel has no light sensor of its own to read. The tablet's is Android's, and the MediaTek
// path the Shows take is not there, so the sensor would only fail and be restarted for as long as the
// app ran.
const Here = false
