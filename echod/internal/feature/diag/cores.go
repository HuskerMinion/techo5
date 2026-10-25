//go:build !panel

package diag

// coresHere: an Echo's daemon may hold cores online through MediaTek's hotplug governor.
const coresHere = true
