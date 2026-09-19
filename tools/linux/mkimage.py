#!/usr/bin/env python3
"""Build the TECHO5 Linux test image for cronos: an Android boot image with the
LineageOS kernel and an initramfs made from the Alpine minirootfs tarball plus
our init and tools. No root, no cpio binary and no extraction step: the cpio is
written straight from the tarball's entries.

    python3 mkimage.py --kernel-image boot-lineage.img --rootfs alpine-minirootfs-armv7.tar.gz \
        --init init --add fbprobe=/usr/local/bin/fbprobe --add audioprobe=/usr/local/bin/audioprobe \
        -o techo5-linux-test.img

The header (load addresses, page size, command line) is copied from the
LineageOS boot image; only the ramdisk is replaced. --cmdline-append adds to
the command line in the header.
"""
import argparse
import gzip
import hashlib
import lzma
import io
import os
import stat
import struct
import sys
import tarfile

NEWC = b"070701"


class Cpio:
    def __init__(self):
        self.buf = io.BytesIO()
        self.ino = 1
        self.seen = set()

    def _entry(self, name, mode, data=b"", nlink=1, rdev=(0, 0), mtime=0):
        if name in self.seen:
            return
        self.seen.add(name)
        nameb = name.encode() + b"\0"
        hdr = NEWC + b"".join(
            b"%08X" % v
            # ino mode uid gid nlink mtime filesize devmajor devminor rdevmajor rdevminor namesize check
            for v in (
                self.ino, mode, 0, 0, nlink, mtime, len(data), 0, 0,
                rdev[0], rdev[1], len(nameb), 0,
            )
        )
        self.ino += 1
        self.buf.write(hdr + nameb)
        self._pad()
        self.buf.write(data)
        self._pad()

    def _pad(self):
        while self.buf.tell() % 4:
            self.buf.write(b"\0")

    def dir(self, name, mode=0o755):
        self._entry(name, stat.S_IFDIR | mode, nlink=2)

    def file(self, name, data, mode=0o644):
        self._entry(name, stat.S_IFREG | mode, data)

    def symlink(self, name, target):
        self._entry(name, stat.S_IFLNK | 0o777, target.encode())

    def chardev(self, name, major, minor, mode=0o600):
        self._entry(name, stat.S_IFCHR | mode, rdev=(major, minor))

    def blockdev(self, name, major, minor, mode=0o600):
        self._entry(name, stat.S_IFBLK | mode, rdev=(major, minor))

    def add_parents(self, name):
        parts = name.strip("/").split("/")[:-1]
        for i in range(1, len(parts) + 1):
            self.dir("/".join(parts[:i]))

    def add_tar(self, path, skip_dotfiles=False):
        with tarfile.open(path, "r:*") as tf:
            for m in tf:
                name = m.name
                while name.startswith("./"):
                    name = name[2:]
                name = name.strip("/")
                if not name:
                    continue
                if skip_dotfiles and name.startswith("."):
                    continue  # apk metadata: .PKGINFO, .SIGN.*, .post-install
                # apk archives may repeat a directory the rootfs already has
                if m.isdir() and name in self.seen:
                    continue
                if m.isdir():
                    self.dir(name, m.mode & 0o7777)
                elif m.issym():
                    self.symlink(name, m.linkname)
                elif m.isfile():
                    self.file(name, tf.extractfile(m).read(), m.mode & 0o7777)
                elif m.islnk():
                    # hard link: store a copy
                    src = tf.getmember(m.linkname)
                    self.file(name, tf.extractfile(src).read(), src.mode & 0o7777)
                elif m.ischr():
                    self.chardev(name, m.devmajor, m.devminor, m.mode & 0o7777)
                # block devices, fifos: not needed

    def finish(self):
        self._entry("TRAILER!!!", 0)
        return self.buf.getvalue()


def read_bootimg(path):
    d = open(path, "rb").read()
    if d[:8] != b"ANDROID!":
        sys.exit(f"{path}: not an Android boot image")
    ks, ka, rs, ra, ss, sa, tl, ps, hv = struct.unpack("<9I", d[8:44])
    if hv != 0:
        sys.exit(f"{path}: header version {hv} not supported")
    pg = lambda n: ((n + ps - 1) // ps) * ps
    hdr = bytearray(d[:ps])
    kernel = d[ps : ps + ks]
    ramdisk = d[ps + pg(ks) : ps + pg(ks) + rs]
    return hdr, kernel, ramdisk, ps


def write_bootimg(hdr, kernel, ramdisk, ps, out, cmdline_append=None):
    pg = lambda n: ((n + ps - 1) // ps) * ps
    struct.pack_into("<I", hdr, 8, len(kernel))
    struct.pack_into("<I", hdr, 16, len(ramdisk))
    struct.pack_into("<I", hdr, 24, 0)  # no second stage
    if cmdline_append:
        cmd = bytes(hdr[64:576]).split(b"\0")[0]
        cmd = (cmd + b" " + cmdline_append.encode()).strip()
        if len(cmd) > 511:
            sys.exit("command line too long")
        hdr[64:576] = cmd + b"\0" * (512 - len(cmd))
    # id: SHA1 over kernel, ramdisk, second and (empty) dtb, each followed by its size,
    # the way mkbootimg fills it. The stock images carry it; keep it valid.
    h = hashlib.sha1()
    for blob in (kernel, ramdisk, b"", b""):
        h.update(blob)
        h.update(struct.pack("<I", len(blob)))
    hdr[576 : 576 + 32] = h.digest() + b"\0" * 12
    img = bytes(hdr) + kernel + b"\0" * (pg(len(kernel)) - len(kernel)) + ramdisk + b"\0" * (pg(len(ramdisk)) - len(ramdisk))
    open(out, "wb").write(img)
    return len(img)


def verify_cpio(data):
    """Walk the archive back and return the entry names, as a sanity check."""
    names = []
    off = 0
    while True:
        if data[off : off + 6] != NEWC:
            raise ValueError(f"bad magic at {off}")
        f = [int(data[off + 6 + i * 8 : off + 14 + i * 8], 16) for i in range(13)]
        namesize, filesize = f[11], f[6]
        name = data[off + 110 : off + 110 + namesize - 1].decode()
        off = (off + 110 + namesize + 3) & ~3
        off = (off + filesize + 3) & ~3
        if name == "TRAILER!!!":
            return names
        names.append(name)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--kernel-image", required=True, help="LineageOS boot.img to take header and kernel from")
    ap.add_argument("--kernel", default=None, metavar="Image.gz-dtb",
                    help="use this kernel blob instead of the one in --kernel-image (header still comes from there)")
    ap.add_argument("--rootfs", default=None, help="Alpine minirootfs .tar.gz (omit for --minimal)")
    ap.add_argument("--minimal", action="store_true",
                    help="no rootfs tarball: the static busybox (from --add) becomes /bin/busybox with applet symlinks")
    ap.add_argument("--init", required=True, help="init script to install as /init")
    ap.add_argument("--add", action="append", default=[], metavar="SRC=DEST", help="extra file (mode 755)")
    ap.add_argument("--apk", action="append", default=[], metavar="FILE.apk",
                    help="Alpine package to unpack into the rootfs (its files only; no install scripts)")
    ap.add_argument("--copy", action="append", default=[], metavar="SRC=DEST", help="extra file, mode 644")
    ap.add_argument("--script", action="append", default=[], metavar="SRC=DEST",
                    help="extra text file, mode 755, CRLF normalised (shell scripts from a Windows checkout)")
    ap.add_argument("--cmdline-append", default=None)
    ap.add_argument("--ramdisk-addr", type=lambda v: int(v, 0), default=None,
                    help="override the ramdisk load address in the header (e.g. 0x43400000)")
    ap.add_argument("--max-size", type=int, default=16 * 1024 * 1024)
    ap.add_argument("--compress", choices=["gzip", "xz"], default="gzip",
                    help="initramfs compression (the LineageOS kernel accepts gzip, lzma, xz and lz4)")
    ap.add_argument("--ramdisk-file", default=None,
                    help="use this ready-made ramdisk instead of building one (control experiments)")
    ap.add_argument("-o", "--output", required=True)
    ap.add_argument("--ramdisk-out", default=None, help="also write the gzip initramfs here")
    a = ap.parse_args()

    if a.ramdisk_file:
        rd = open(a.ramdisk_file, "rb").read()
        hdr, kernel, old_rd, ps = read_bootimg(a.kernel_image)
        if a.kernel:
            kernel = open(a.kernel, "rb").read()
        if a.ramdisk_addr is not None:
            struct.pack_into("<I", hdr, 20, a.ramdisk_addr)
        size = write_bootimg(hdr, kernel, rd, ps, a.output, a.cmdline_append)
        print(f"ramdisk from file: {len(rd)} bytes; image: {size} bytes")
        return

    c = Cpio()
    c.dir("dev"); c.chardev("dev/console", 5, 1); c.chardev("dev/null", 1, 3, 0o666)
    c.chardev("dev/kmsg", 1, 11); c.chardev("dev/fb0", 29, 0)
    # eMMC partitions init needs before mdev runs: MISC (breadcrumbs), system, userdata
    for n in (8, 12, 16):
        c.blockdev(f"dev/mmcblk0p{n}", 179, n)
    c.dir("proc"); c.dir("sys"); c.dir("tmp", 0o1777); c.dir("run"); c.dir("data"); c.dir("android")
    if a.rootfs:
        c.add_tar(a.rootfs)
    for apk in a.apk:
        c.add_tar(apk, skip_dotfiles=True)
    for spec in a.copy:
        src, dest = spec.split("=", 1)
        dest = dest.strip("/")
        c.add_parents(dest)
        c.file(dest, open(src, "rb").read().replace(b"\r\n", b"\n"), 0o644)
    for spec in a.script:
        src, dest = spec.split("=", 1)
        dest = dest.strip("/")
        c.add_parents(dest)
        c.file(dest, open(src, "rb").read().replace(b"\r\n", b"\n"), 0o755)
    init = open(a.init, "rb").read().replace(b"\r\n", b"\n")
    c.file("init", init, 0o755)
    adds = {}
    for spec in a.add:
        src, dest = spec.split("=", 1)
        dest = dest.strip("/")
        adds[dest] = src
        c.add_parents(dest)
        c.file(dest, open(src, "rb").read(), 0o755)
    if a.minimal:
        if not a.rootfs and "bin/busybox.static" not in adds:
            sys.exit("--minimal needs --add <busybox.static>=/bin/busybox.static")
        for d in ("bin", "sbin", "usr/bin", "usr/sbin", "etc", "lib", "root", "usr/local/bin"):
            c.dir(d)
        c.symlink("bin/busybox", "busybox.static")
        for app in ("sh mount umount mkdir ln ls wc cat echo sleep dd printf uname mdev mknod tee head tail "
                    "grep cut date dmesg ip insmod sync cp reboot setsid ifconfig tr od free cttyhack").split():
            c.symlink(f"bin/{app}", "busybox")
        c.file("etc/passwd", b"root::0:0:root:/root:/bin/sh\n")
    raw = c.finish()
    names = verify_cpio(raw)
    if "init" not in names or "bin/busybox" not in names or "bin/busybox.static" not in names:
        sys.exit("initramfs is missing /init, /bin/busybox or /bin/busybox.static (pass --add busybox.static=/bin/busybox.static)")
    if a.compress == "xz":
        # the kernel's xz decompressor wants CRC32 checks and no BCJ filter
        rd = lzma.compress(raw, format=lzma.FORMAT_XZ, check=lzma.CHECK_CRC32,
                           filters=[{"id": lzma.FILTER_LZMA2, "preset": 9 | lzma.PRESET_EXTREME, "dict_size": 32 << 20}])
    else:
        rd = gzip.compress(raw, 9)
    if a.ramdisk_out:
        open(a.ramdisk_out, "wb").write(rd)

    hdr, kernel, old_rd, ps = read_bootimg(a.kernel_image)
    if a.kernel:
        kernel = open(a.kernel, "rb").read()
    if a.ramdisk_addr is not None:
        struct.pack_into("<I", hdr, 20, a.ramdisk_addr)
    size = write_bootimg(hdr, kernel, rd, ps, a.output, a.cmdline_append)
    print(f"initramfs: {len(names)} entries, {len(raw)} bytes raw, {len(rd)} bytes gzip")
    print(f"kernel: {len(kernel)} bytes; image: {size} bytes ({size / a.max_size:.0%} of {a.max_size})")
    if size > a.max_size:
        sys.exit("image does not fit the partition")


if __name__ == "__main__":
    main()
