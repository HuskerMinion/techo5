//go:build dot

package wifiwatch

// watchBroadcasts is whether this device watches broadcasts on their own (broadcast.go). The Dots: their
// Wi-Fi firmware keeps a group key's replay counter across a rekey and drops broadcasts until the
// router's count passes it (techo5-dot#45). A device without that fault has no reason to reassociate
// a working link because a quiet network went quieter.
const watchBroadcasts = true

// refreshAfterReassociate is whether a reassociation is followed by a refresh of the receive filter
// (rejoin.go): the Dots' driver drops most multicast after one (techo5-dot#45).
const refreshAfterReassociate = true
