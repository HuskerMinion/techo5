package bluetooth

import (
	"encoding/binary"
)

const (
	manufacturerDataType = 0xff
	appleCompanyID       = 0x004c
	iBeaconType          = 0x02
	iBeaconPayloadLength = 0x15
	iBeaconMajor         = 1
	iBeaconMeasuredPower = -59
)

// This randomly assigned UUID identifies EchoLocal receivers. BPS distinguishes individual
// receivers by the complete UUID, major, and minor tuple.
var iBeaconUUID = [16]byte{
	0x9c, 0x5f, 0xa6, 0xf1, 0x91, 0xc4, 0x4f, 0x56,
	0xbb, 0x9f, 0xd9, 0x2a, 0xcf, 0xd9, 0xd4, 0x0b,
}

// beaconAdvertisement formats an iBeacon advertisement for minor.
func beaconAdvertisement(minor int) []byte {
	if minor < 0 || minor > 0xffff {
		return nil
	}

	// No flags at all (#773). The beacon goes out non-connectable (ADV_NONCONN_IND), where the Core
	// specification leaves the flags out, and a flag is what makes a phone list it: "general
	// discoverable" had Android show the device in its Bluetooth settings at all times, as if it
	// were open for pairing, which it never is from the beacon. "BR/EDR not supported", the usual
	// iBeacon flag, was worse: the beacon goes out from the device's own address, which is also its
	// classic Bluetooth, and a phone took the device for a low energy one and never offered it as a
	// speaker to pair with. BPS and the like go by the UUID, major and minor, not the flags; pairing
	// mode is found by classic inquiry, as before.
	var advertisement []byte
	manufacturerLength := len(advertisement)
	advertisement = append(advertisement, 0, manufacturerDataType)
	advertisement = binary.LittleEndian.AppendUint16(advertisement, appleCompanyID)
	advertisement = append(advertisement, iBeaconType, iBeaconPayloadLength)
	advertisement = append(advertisement, iBeaconUUID[:]...)
	advertisement = binary.BigEndian.AppendUint16(advertisement, iBeaconMajor)
	advertisement = binary.BigEndian.AppendUint16(advertisement, uint16(minor))
	measuredPower := int8(iBeaconMeasuredPower)
	advertisement = append(advertisement, byte(measuredPower))
	advertisement[manufacturerLength] = byte(len(advertisement) - manufacturerLength - 1)
	return advertisement
}
