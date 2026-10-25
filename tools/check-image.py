#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Scan an image that is about to be published for what must never ship in one.

    check-image.py [--deny WORD]... [--no-file NAME]... [--models NAME,...] [--empty-keys] IMAGE

IMAGE is a filesystem image (ext4, as mkstore.sh makes it), raw or gzipped, or a root filesystem
tarball (.tar.gz). release.ps1 runs this on athena's and the Skylight's store images and rootfs
tarballs before it publishes them. It reads the image as one byte stream, so it needs nothing but
Python, on any computer. Fails on:

  - an SSH public key line, or the body of an OpenSSH private key;
  - --deny WORD anywhere, without regard to case, as a word of its own: the builder's user name,
    the computer's name, the checkout's path. A WORD right after github.com/ is a Go module path,
    not the builder, and is let through;
  - --no-file NAME: a file of that name (a directory entry in an ext4 image, a member of a tarball),
    such as the unit's own Wi-Fi firmware, which is the vendor's and never ships;
  - --models: a wake word model (*.tflite) whose name is not on the list: the image carries only the
    public models tools/fetch-inputs.py fetches;
  - --empty-keys: a root/.ssh/authorized_keys that holds anything (a tarball only; in an ext4 image a
    key there is caught as a key line).

Prints what it found and exits 1, or prints OK and exits 0.
"""
import argparse
import gzip
import re
import sys
import tarfile
import zlib

CHUNK = 1 << 20
TAIL = 4096
KEYS = re.compile(rb"(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp\d+|sk-ssh-ed25519@openssh\.com) AAAA"
                  rb"|b3BlbnNzaC1rZXktdjE")  # base64 of "openssh-key-v1", the start of every OpenSSH private key
# Not PEM private keys: shairport-sync carries the AirPort Express's RSA key, which AirPlay needs and
# everyone has.
# An ext4 directory entry: the name's length, the file type (1 regular, 7 symlink), the name.
DIRENT_MODEL = re.compile(rb"([\x01-\xff])[\x01\x07]([A-Za-z0-9_.!-]{1,250}?\.tflite)")


def stream(path):
    """The image's bytes, decompressed when it is gzipped, in chunks."""
    with open(path, "rb") as f:
        gz = f.read(2) == b"\x1f\x8b"
    opener = gzip.open if gz else open
    with opener(path, "rb") as f:
        while True:
            b = f.read(CHUNK)
            if not b:
                return
            yield b


def is_tar(path):
    try:
        with tarfile.open(path, "r:*") as t:
            return t.next() is not None
    except (tarfile.TarError, OSError, EOFError, zlib.error):
        return False


def scan(path, deny=(), no_file=(), models=None, empty_keys=False):
    """What was found, as lines for people."""
    found = []
    words = [w for w in deny if w and len(w) >= 3]
    deny_re = None
    if words:
        alt = b"|".join(re.escape(w.encode()) for w in sorted(set(words), key=len, reverse=True))
        deny_re = re.compile(rb"(?i)(?<![A-Za-z0-9])(" + alt + rb")(?![A-Za-z0-9])")
    counts = {}
    tail = b""
    seen_models = set()
    for chunk in stream(path):
        buf = tail + chunk
        # Matches wholly inside the tail were seen with the chunk before; take only those that end in
        # this chunk.
        cut = len(tail)
        for m in KEYS.finditer(buf):
            if m.end() > cut:
                counts["an SSH key"] = counts.get("an SSH key", 0) + 1
        if deny_re:
            for m in deny_re.finditer(buf):
                if m.end() <= cut:
                    continue
                if buf[max(0, m.start() - 11):m.start()].lower() == b"github.com/":
                    continue
                what = "%r" % m.group(1).decode("utf-8", "replace").lower()
                counts[what] = counts.get(what, 0) + 1
        for m in DIRENT_MODEL.finditer(buf):
            if m.end() > cut and m.group(1)[0] == len(m.group(2)):
                seen_models.add(m.group(2).decode())
        for name in no_file:
            n = name.encode()
            if len(n) < 256:
                pat = re.compile(re.escape(bytes([len(n)])) + rb"[\x01\x07]" + re.escape(n))
                for m in pat.finditer(buf):
                    if m.end() > cut:
                        counts["a file named %s" % name] = counts.get("a file named %s" % name, 0) + 1
        tail = buf[-TAIL:]
    if is_tar(path):
        with tarfile.open(path, "r:*") as t:
            for m in t:
                base = m.name.rstrip("/").rsplit("/", 1)[-1]
                if base in no_file:
                    found.append("a file named %s: %s" % (base, m.name))
                if base.endswith(".tflite"):
                    seen_models.add(base)
                if empty_keys and m.name.lstrip("./").endswith("root/.ssh/authorized_keys") and m.size:
                    found.append("%s is not empty" % m.name)
        # A tarball's names were read from its headers just now; the directory entry pattern is for
        # ext4 images only.
        for k in [k for k in counts if k.startswith("a file named ")]:
            del counts[k]
    for what, n in sorted(counts.items()):
        found.append("%s (%d)" % (what, n))
    if models is not None:
        extra = sorted(seen_models - set(models))
        if extra:
            found.append("wake word models that are not the public ones: %s" % ", ".join(extra))
    return found


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--deny", action="append", default=[])
    ap.add_argument("--no-file", action="append", default=[])
    ap.add_argument("--models", help="the allowed wake word models, comma separated (name.tflite)")
    ap.add_argument("--empty-keys", action="store_true")
    ap.add_argument("image")
    a = ap.parse_args(argv[1:])
    models = None
    if a.models is not None:
        models = [m.strip() for m in a.models.split(",") if m.strip()]
    try:
        found = scan(a.image, a.deny, a.no_file, models, a.empty_keys)
    except (OSError, EOFError, zlib.error) as e:
        print("FAIL: %s: %s" % (a.image, e))
        return 1
    for f in found:
        print("FAIL: %s carries %s" % (a.image, f))
    if found:
        return 1
    print("OK: %s: no keys, nothing denied, no files it must not have%s" % (
        a.image, "" if models is None else ", only the public wake word models"))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
