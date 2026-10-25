#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check a Skylight boot image made by pack-sky.py (build.sh runs this on its output).

    check-boot.py [--installer [--kernel-of boot.img]] image

Checks: every header field against pack-sky.py's constants (header version 2, the stock load
addresses), the SHA-1 id, the layout (header page, kernel, ramdisk, resource image, device tree, each
page aligned, zero padding), that the kernel is an arm64 Image, that the device tree is a valid FDT
for this board (skylight,cal150), that the resource image holds that same device tree under every
name the stock U-Boot looks for and the logo, and that the image fits the stock recovery partition.

The boot image (the release's, and what a rootfs carries for the kernel update): the command line
mounts techo5-store as the root, and the ramdisk is empty. With --installer, the installer image:
the command line runs the ramdisk's /init with techo5.installer=1, and the ramdisk holds exactly
the installer's files (build.sh, INSTALLER=1), the techo5-data image matching its own size and sum,
and no SSH key or firmware; with --kernel-of, its kernel, device tree and resource image are that
boot image's, so the one source tarball is the source of both.
"""
import gzip
import hashlib
import importlib.util
import os
import struct
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("pack_sky", os.path.join(HERE, "pack-sky.py"))
pack = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pack)

FDT_MAGIC = 0xD00DFEED
HEADER_SIZE = 1660
COMPATIBLE = b"skylight,cal150\0"
# The recovery partition of the stock D156 table: 262144 sectors.
RECOVERY_BYTES = 128 << 20
LOGO_SIZE = (1920, 1080)

# What the installer image's ramdisk holds (build.sh, INSTALLER=1), files only.
INSTALLER_FILES = {
    "init", "SOURCES", "bin/busybox", "bin/reboot-normal",
    "installer/install.sh", "installer/data.img.gz", "installer/data.img.info",
}
INSTALLER_EXEC = {"init", "bin/busybox", "bin/reboot-normal", "installer/install.sh"}
# Never in any of them: SSH keys, and firmware (the unit's own is the vendor's, never shipped).
FORBIDDEN_NAMES = (".ssh", "authorized_keys", "firmware", "brcm", ".hcd", "nvram")
KEY = (b"ssh-ed25519 AAAA", b"ssh-rsa AAAA", b"ssh-dss AAAA", b"ecdsa-sha2-nistp")


def cpio_entries(cpio):
    """newc cpio -> {name: (mode, data)}. Raises ValueError on a malformed archive."""
    entries = {}
    p = 0
    while True:
        if cpio[p:p + 6] != b"070701":
            raise ValueError("bad cpio header at %d" % p)
        f = [int(cpio[p + 6 + 8 * i:p + 14 + 8 * i], 16) for i in range(13)]
        mode, uid, gid, filesize, namesize = f[1], f[2], f[3], f[6], f[11]
        name = cpio[p + 110:p + 110 + namesize - 1].decode()
        p += 110 + namesize
        p += -p % 4
        data = cpio[p:p + filesize]
        if len(data) != filesize:
            raise ValueError("cpio ends inside %s" % name)
        p += filesize
        p += -p % 4
        if name == "TRAILER!!!":
            break
        if uid or gid:
            raise ValueError("%s is owned by %d:%d, not 0:0" % (name, uid, gid))
        entries[name[2:] if name.startswith("./") else name] = (mode, data)
    if any(cpio[p:]):
        raise ValueError("data after the cpio trailer")
    return entries


def resource_entries(r):
    """[(name, data)] from a Rockchip RSCE resource image, each entry's SHA-1 checked."""
    if r[:4] != b"RSCE":
        raise ValueError("not a Rockchip resource image")
    hdr_blks, tbl_blks, count = r[8], r[10], struct.unpack_from("<I", r, 12)[0]
    out = []
    for i in range(count):
        e = r[(hdr_blks + i * tbl_blks) * pack.BLK:][:pack.BLK]
        if e[:4] != b"ENTR":
            raise ValueError("entry %d is not an ENTR" % i)
        name = e[4:224].split(b"\0")[0].decode()
        sha1 = e[224:244]
        off, size = struct.unpack_from("<II", e, 260)
        data = r[off * pack.BLK:off * pack.BLK + size]
        if len(data) != size:
            raise ValueError("%s ends past the resource image" % name)
        if hashlib.sha1(data).digest() != sha1:
            raise ValueError("%s does not match its SHA-1" % name)
        out.append((name, data))
    return out


def check(img, installer=False):
    errors = []

    def want(cond, msg):
        if not cond:
            errors.append(msg)

    page = pack.PAGE
    if len(img) < page:
        return ["shorter than one page"]
    (magic, ksize, kaddr, rsize, raddr, ssize, saddr, tags, pg, hv, osv) = struct.unpack_from("<8s10I", img, 0)
    name = img[48:64]
    cmd = img[64:576]
    digest = img[576:608]
    extra = img[608:1632]
    dtbo_size, dtbo_off, hsize, dtb_size, dtb_addr = struct.unpack_from("<IQIIQ", img, 1632)
    want(magic == b"ANDROID!", "magic %r" % magic)
    want(kaddr == pack.KERNEL_ADDR, "kernel_addr %#x" % kaddr)
    want(raddr == pack.RAMDISK_ADDR, "ramdisk_addr %#x" % raddr)
    want(saddr == pack.SECOND_ADDR, "second_addr %#x" % saddr)
    want(tags == pack.TAGS_ADDR, "tags_addr %#x" % tags)
    want(pg == page, "page_size %d" % pg)
    want(hv == 2, "header_version %d" % hv)
    want(osv == pack.OS_VERSION, "os_version %#x" % osv)
    want(not any(name), "name %r" % name.rstrip(b"\0"))
    want(not any(extra), "extra_cmdline is not empty")
    want(dtbo_size == 0 and dtbo_off == 0, "recovery_dtbo %d@%d" % (dtbo_size, dtbo_off))
    want(hsize == HEADER_SIZE, "header_size %d" % hsize)
    want(dtb_addr == pack.DTB_ADDR, "dtb_addr %#x" % dtb_addr)
    want(ksize > 0 and rsize > 0 and ssize > 0 and dtb_size > 0, "an empty part")
    want(not any(img[HEADER_SIZE:page]), "header page padding is not zero")
    if errors:
        return errors

    def pad(n):
        return (n + page - 1) // page * page

    k_at = page
    r_at = k_at + pad(ksize)
    s_at = r_at + pad(rsize)
    d_at = s_at + pad(ssize)
    end = d_at + pad(dtb_size)
    want(len(img) == end, "image size %d, expected %d" % (len(img), end))
    want(len(img) <= RECOVERY_BYTES, "larger than the recovery partition")
    if len(img) < end:
        return errors
    kernel = img[k_at:k_at + ksize]
    ramdisk = img[r_at:r_at + rsize]
    second = img[s_at:s_at + ssize]
    dtb = img[d_at:d_at + dtb_size]
    for at, size, nxt, part in ((k_at, ksize, r_at, "kernel"), (r_at, rsize, s_at, "ramdisk"),
                                (s_at, ssize, d_at, "resource image"), (d_at, dtb_size, end, "device tree")):
        want(not any(img[at + size:nxt]), "padding after the %s is not zero" % part)
    h = hashlib.sha1()
    for blob in (kernel, ramdisk, second, b"", dtb):
        h.update(blob)
        h.update(struct.pack("<I", len(blob)))
    want(digest == h.digest().ljust(32, b"\0"), "SHA-1 id does not match the contents")

    want(kernel[0x38:0x3c] == b"ARM\x64", "the kernel is not an arm64 Image")
    want(len(dtb) >= 40 and struct.unpack_from(">I", dtb, 0)[0] == FDT_MAGIC, "the device tree is not an FDT")
    if len(dtb) >= 8:
        want(struct.unpack_from(">I", dtb, 4)[0] == len(dtb), "FDT totalsize does not match")
    want(COMPATIBLE in dtb, "the device tree is not the Skylight's (skylight,cal150)")

    try:
        res = resource_entries(second)
        names = [n for n, _ in res]
        want(sorted(names) == sorted(list(pack.DTB_NAMES) + ["logo.bmp"]), "resource entries %s" % names)
        for n, data in res:
            if n in pack.DTB_NAMES:
                want(data == dtb, "resource %s is not the image's device tree" % n)
            elif n == "logo.bmp":
                want(data[:2] == b"BM" and len(data) >= 26 and
                     struct.unpack_from("<ii", data, 18) == LOGO_SIZE, "logo.bmp is not a %dx%d BMP" % LOGO_SIZE)
    except (ValueError, UnicodeDecodeError, struct.error) as e:
        errors.append("resource image: %s" % e)

    line = cmd.split(b"\0")[0]
    words = line.decode("ascii", "replace").split()
    want(not any(cmd[len(line):]), "command line has bytes after its end")
    if installer:
        for w in ("rdinit=/init", "techo5.installer=1"):
            want(w in words, "command line has no %s" % w)
        want(not any(w.startswith("root=") for w in words), "the installer's command line names a root")
    else:
        for w in ("root=PARTLABEL=techo5-store", "init=/init", "ro", "rootwait", "panic=10"):
            want(w in words, "command line has no %s" % w)
        want(not any(w.startswith("rdinit=") or w.startswith("techo5.") for w in words),
             "the boot image's command line has installer words: %s" % " ".join(words))

    try:
        d = zlib.decompressobj(16 + zlib.MAX_WBITS)
        cpio = d.decompress(ramdisk)
        want(d.eof and not d.unused_data, "the ramdisk is not one whole gzip stream")
        ents = cpio_entries(cpio)
        files = {n: (m, data) for n, (m, data) in ents.items() if m & 0o170000 != 0o040000}
        bad = [n for n in ents if any(w in n for w in FORBIDDEN_NAMES)]
        want(not bad, "the ramdisk carries %s" % bad)
        for pat in KEY:
            want(pat not in cpio, "the ramdisk carries an SSH key (%s)" % pat.decode())
        if installer:
            want(set(files) == INSTALLER_FILES, "installer ramdisk files %s" % sorted(files))
            for n in INSTALLER_EXEC & set(files):
                want(files[n][0] & 0o170111 == 0o100111, "%s is not an executable file" % n)
            info = files.get("installer/data.img.info", (0, b""))[1].split()
            gz = files.get("installer/data.img.gz", (0, b""))[1]
            try:
                data = gzip.decompress(gz)
                want(len(info) == 2 and info[0] == str(len(data)).encode() and
                     info[1] == hashlib.sha256(data).hexdigest().encode(),
                     "installer/data.img.info does not describe installer/data.img.gz")
                want(data[1024 + 56:1024 + 58] == b"\x53\xef" and
                     data[1024 + 120:1024 + 136].rstrip(b"\0") == b"techo5-data",
                     "installer/data.img.gz is not an ext4 labeled techo5-data")
            except (OSError, EOFError, zlib.error) as e:
                errors.append("installer/data.img.gz: %s" % e)
            src = files.get("SOURCES", (0, b""))[1]
            want(b"bin/busybox: BusyBox v" in src and b"Source: " in src, "SOURCES does not say where busybox's source is")
        else:
            want(not files, "the boot image's ramdisk is not empty: %s" % sorted(files))
    except (zlib.error, ValueError, UnicodeDecodeError) as e:
        errors.append("ramdisk: %s" % e)
    return errors


def parts(img):
    """The kernel, resource image and device tree of a header v2 image, for comparing two."""
    ksize, rsize, ssize = (struct.unpack_from("<I", img, at)[0] for at in (8, 16, 24))
    dsize = struct.unpack_from("<I", img, 1648)[0]
    page = pack.PAGE

    def pad(n):
        return (n + page - 1) // page * page
    k = page
    s = k + pad(ksize) + pad(rsize)
    d = s + pad(ssize)
    return img[k:k + ksize], img[s:s + ssize], img[d:d + dsize]


def main(argv):
    args = argv[1:]
    installer = "--installer" in args
    args = [a for a in args if a != "--installer"]
    boot = None
    if "--kernel-of" in args:
        i = args.index("--kernel-of")
        if not installer or i + 1 >= len(args):
            print("check-boot.py: --kernel-of goes with --installer and names the boot image", file=sys.stderr)
            return 2
        boot = args[i + 1]
        del args[i:i + 2]
    if len(args) != 1:
        print("usage: check-boot.py [--installer [--kernel-of boot.img]] image", file=sys.stderr)
        return 2
    with open(args[0], "rb") as f:
        img = f.read()
    errors = check(img, installer)
    if boot is not None and not errors:
        with open(boot, "rb") as f:
            b = f.read()
        if check(b):
            errors.append("%s is not a Skylight boot image" % boot)
        elif parts(b) != parts(img):
            errors.append("the kernel, device tree or resource image is not %s's" % boot)
    for e in errors:
        print("FAIL:", e)
    if errors:
        return 1
    print("OK: %s, %d bytes, %s image, ramdisk %d bytes" % (
        args[0], len(img), "installer" if installer else "boot", struct.unpack_from("<I", img, 16)[0]))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
