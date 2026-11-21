package wake

// The wake words the image carries are copied into the model directory beside everything else (the
// Linux images at boot, from /usr/share/techo5/models; the Fire OS installer when it provisions a
// device), so where a model sits says nothing about where it came from. Their bytes do: every image
// and installer takes them from the same pinned commits and checks each file against its sha256
// before it goes in.
//
// Knowing them matters when Home Assistant's custom_wake_words holds a byte-identical copy of one,
// as it does when somebody drops the published okay_nabu there. Without this the copy was taken for a
// model adopted before Adopt marked what it fetched, marked as Home Assistant's, and moved onto the
// exact front end, where its sensitivity settings no longer meant what they did.

// shipped is the sha256 of every model file the image carries, as tools/fetch-inputs.py lists them in
// MODEL_SHA256 (tools/install-cronos.ps1 installs a subset of the same pins). A test checks the two
// agree, so moving a pin there without moving it here fails a build rather than a device. A variable
// so that a test can stand a model of its own in for one.
var shipped = map[string]bool{
	"0689abe1912a95a3318a0d8cb2e67bad0cbcfe3e24dd6e050c75debddfb6f891": true, // okay_nabu
	"21a7976add39ee24ec96c63d96b7aaa18e24d1d9824b963e451da8feb4b78b77": true, // hey_jarvis
	"c2a9b6ed51182db72e014781d5a4ece1929dc232a40b5b4be384f0295f0e1571": true, // hey_mycroft
	"9011a8155b04de858c48038529235cbc0e42e9fca05a55bf588cb80a653a723b": true, // alexa
	"411db364955bf7b7a13a50d732a8b59c129e2fbe130a54f9eb3c20ca183bc4d0": true, // computer
	"cb2102fc9a76d4e02a740760d5ba2060978d766869489000b3565c8c4f8493a5": true, // jarvis
	"eb127d82d884a1ef4167b455ec67682bf362b9de3e232c3f8d544a5e6ab4cb8b": true, // hey_friday
	"7564b95e5deed29cecfd55fd34cac70da5307c0d86108a7f87bfc610c9724dec": true, // glados
	"8ddbdc859eed8fbd648f4b5fe137f82e5e8480c1756fd12257e3ef5645275117": true, // hal
	"7feb69397a56a6933d3248ecca3ca6aaca7b6bf2c36f5beb9b63bb3d17647686": true, // terminator
	"ed91c4d83e28bcc0af1cdebe3ab4f2a4f2d4c9908101ad8476d2e1b9b77f4e0c": true, // marvin
	"9f54305884abde30d484f18dbff582ed6e2f1a141b93d6fa5f7feb505a0c94aa": true, // home_assistant
}

// fromImage is whether a model with this sha256 is one the image carries. An offer that Have finds on
// disk has the file's own hash, so asking with the offer's is asking about the file.
func fromImage(hash string) bool { return shipped[hash] }
