package kernel

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
)

// U-Boot on athena (the boot layer: u-boot-amazon-echo, tools/linux/README.md): once U-Boot is in the
// boot partition, it starts each slot's own kernel from the slot, and its A/B record does what the
// kernel trial here does. This package knows nothing of that: the OS does not run the kernel update
// where the boot layer is (etc/techo5/boot.sh, bootctl current). What stays here is the safety net:
// nothing here ever writes over a boot partition that does not hold TECHO5's athena kernel, U-Boot
// least of all, whoever calls it.

// ubootMarker is in every TECHO5 athena U-Boot build (u-boot-amazon-echo's CONFIG_AB_SELF_MARKER,
// as TECHO5's athena build sets it).
var ubootMarker = []byte("techo5-athena-uboot-" + "image")

// errNotKernel is the refusal to write over anything but TECHO5's athena kernel.
var errNotKernel = errors.New("not TECHO5's athena kernel")

// athenaKernel checks that a boot image holds TECHO5's athena kernel: its kernel part gunzips to an
// arm64 Image of Linux, with no U-Boot marker in it. U-Boot's image (gzip'd too, with the same kind
// of device tree after it, tuning slot and all) fails it, as does anything else.
func athenaKernel(im *image) error {
	k := im.part(im.kernelAt, im.kernelSize)
	if bytes.Contains(k, ubootMarker) {
		return errors.New("it holds TECHO5's U-Boot (uncompressed)")
	}
	zr, err := gzip.NewReader(bytes.NewReader(k))
	if err != nil {
		return errors.New("its kernel part is not gzip'd")
	}
	zr.Multistream(false) // the device tree follows the stream
	out, err := io.ReadAll(io.LimitReader(zr, 128<<20))
	if err != nil {
		return errors.New("its kernel part does not gunzip")
	}
	switch {
	case bytes.Contains(out, ubootMarker):
		return errors.New("it holds TECHO5's U-Boot")
	case len(out) < 64 || !bytes.Equal(out[56:60], []byte("ARM\x64")):
		return errors.New("its kernel is not an arm64 Image")
	case !bytes.Contains(out, []byte("Linux version ")):
		return errors.New("its kernel is not Linux")
	}
	return nil
}

// checkBootPartition is the guard before any write to athena's boot partition: what is there now
// must be TECHO5's athena kernel. (Amazon's is replaced by the installer only, never by this code.)
func (p Paths) checkBootPartition(part []byte) error {
	if p.Board != Athena {
		return nil
	}
	cur, err := parseImage(part)
	if err != nil {
		return errors.Join(errNotKernel, err)
	}
	if err := athenaKernel(cur); err != nil {
		return errors.Join(errNotKernel, err)
	}
	return nil
}
