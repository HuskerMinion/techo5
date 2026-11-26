//go:build !panel

package addons

// Here is true off a Panel: the Linux images run add-ons from userdata (on the Dot, only while it runs
// TECHO5's Linux rather than Fire OS; Run checks).
const Here = true
