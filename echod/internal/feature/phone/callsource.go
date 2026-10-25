//go:build !panel

package phone

// checkCallSource: an Echo rings for every call the listener is offered. Its firewall already lets in
// only the provider's side of the registration, and a check here would only add ways to miss a call: a
// name lookup that fails or is slow when the call arrives would refuse it.
const checkCallSource = false
