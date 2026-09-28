//go:build !dot && !spot

package home

// hasGlance is whether this device draws the glance strip: the Show. The Spot has a screen but no
// strip on its round face, so there the entities are not followed and the action is not offered.
const hasGlance = true
