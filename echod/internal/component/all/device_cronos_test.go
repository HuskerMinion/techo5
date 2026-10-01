//go:build !dot && !spot

package all

// notOnThisDevice is empty on the Echo Show 5: registered is its whole list.
var notOnThisDevice []string

var deviceSpecific []string
