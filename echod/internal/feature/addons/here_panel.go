//go:build panel

package addons

// Here is false on a TECHO5 Panel: it is an Android app (targetSdk 34), and Android 10 and later do not
// let an app targeting 29 or above run a file it downloaded, so there is nothing an add-on could be
// started as. Its setup page says so in a line instead of the card.
const Here = false

// family is no family: no catalog entry names the Panel.
const family = ""
