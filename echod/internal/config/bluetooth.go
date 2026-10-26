package config

// Bluetooth is whether the device also acts as a BLE proxy for Home Assistant, and which audio
// device (earbuds, a speaker) it plays to when that device is around.
type Bluetooth struct {
	Proxy bool `json:"proxy"`

	// Audio is the last audio device paired from this device: its address and the name it gave,
	// so the daemon can try it again after a reboot and the screen can say what it is waiting for.
	Audio     string `json:"audio,omitempty"`
	AudioName string `json:"audio_name,omitempty"`

	// SecureConnectionsOff turns the controller's Secure Connections off (btmgmt sc off), for a phone
	// that pairs and is dropped a second or two later with "Authentication Failed" while it is on
	// (#183: a POCO F8 Pro on Android 16). Off is the exception, so the zero value, and a file from
	// before the setting, is on: what every device did until then. This device's own, as the rest
	// of Bluetooth is (KeepOwn): which phones pair with it is a matter of this one's radio.
	SecureConnectionsOff bool `json:"secure_connections_off,omitempty"`

	// ProxyConnections lets Home Assistant connect to BLE devices through the proxy, not only hear them
	// (the Show and the Spot). Off unless asked for: with it on, Home Assistant routes a lock or a
	// sensor through the device whenever it has the best signal, and many BLE devices take one
	// connection only.
	ProxyConnections bool `json:"proxy_connections,omitempty"`
}

// Off unless asked for: it keeps a radio scanning that shares an antenna with wifi.
const DefaultBluetoothProxy = false

func defaultBluetooth() Bluetooth {
	return Bluetooth{Proxy: DefaultBluetoothProxy}
}

type BluetoothWriter struct{ st *Store }

func (w BluetoothWriter) Proxy(v bool) error {
	return w.st.Update(func(c *Config) { c.Bluetooth.Proxy = v })
}

// ProxyConnections is whether the proxy lends Home Assistant connections.
func (w BluetoothWriter) ProxyConnections(v bool) error {
	return w.st.Update(func(c *Config) { c.Bluetooth.ProxyConnections = v })
}

// SecureConnections is the controller's Secure Connections: on, as it comes, or off.
func (w BluetoothWriter) SecureConnections(on bool) error {
	return w.st.Update(func(c *Config) { c.Bluetooth.SecureConnectionsOff = !on })
}

// Audio remembers the audio device; empty forgets it.
func (w BluetoothWriter) Audio(address, name string) error {
	return w.st.Update(func(c *Config) { c.Bluetooth.Audio, c.Bluetooth.AudioName = address, name })
}
