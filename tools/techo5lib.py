"""Shared by the TECHO5 installers and tools: output, adb and fastboot, checked downloads, and the
units' USB serial console, on Windows, Linux and macOS with nothing but Python 3's standard library.

The same file is in techo5, techo5-dot and techo5-spot (tools/techo5lib.py); keep the copies identical.
"""
import base64
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request

IS_WINDOWS = os.name == 'nt'
IS_MACOS = sys.platform == 'darwin'

# Alpine's base image, pinned: boot images' initramfs is built on it.
ALPINE_URL = 'https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/armv7/alpine-minirootfs-3.24.1-armv7.tar.gz'
ALPINE_SHA256 = '50942d567e6ee422c16cb46d5c282ed9d8adc9007c2a483faf4148a18c64ce32'

# The public half of the key releases are signed with, the same one the daemon's updater trusts
# (echod/internal/update/trust.go, releaseKey). An installer writes a root filesystem to a unit, so a
# manifest is believed only when this key signed it: HTTPS alone would let anything that can present a
# certificate this computer accepts hand the installer a root filesystem of its own.
RELEASE_KEY = 'KVUuQUbhyKwPBbIneqFEvXYSI+3Hkfu/heCTy5YNVMk='

# The USB serial consoles: TECHO5 Linux on the Show 5 and the Spot (Linux Foundation ids, told apart by
# the serial number on the kernel command line), and on the Dot (Google ids, with the unit's serial
# number as the USB serial).
CONSOLE_TECHO5 = ('1d6b', '0104')
CONSOLE_DOT = ('18d1', '4ee7')


class Fail(Exception):
    """A reason to stop, said plainly."""


def fail(message):
    raise Fail(message)


def step(what):
    print('== ' + what, flush=True)


def note(what):
    print('   ' + what, flush=True)


def run_main(fn):
    """Runs a tool's main, turning a Fail into one line and exit status 1."""
    try:
        fn()
    except Fail as e:
        print('error: %s' % e, file=sys.stderr, flush=True)
        sys.exit(1)
    except KeyboardInterrupt:
        print('\nstopped', file=sys.stderr)
        sys.exit(130)


def repo_root():
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def default_dir(env, name):
    """A work folder: the environment variable when set, else <repository>/<name>."""
    return os.path.abspath(os.environ.get(env) or os.path.join(repo_root(), name))


def tool(exe):
    """A command line for a tool: a .py path runs with this Python (used by rehearsals)."""
    if exe.endswith('.py'):
        return [sys.executable, exe]
    return [exe]


def need(exe, hint):
    if exe.endswith('.py') and os.path.exists(exe):
        return
    if not shutil.which(exe) and not os.path.exists(exe):
        fail('%s not found: %s' % (exe, hint))


def hash_file(path, algo='sha256'):
    h = hashlib.new(algo)
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.hexdigest()


def md5(path):
    return hash_file(path, 'md5')


def head_is_android(path):
    with open(path, 'rb') as f:
        return f.read(8) == b'ANDROID!'


# ------------------------------------------------------------------------------------------ downloads

def _open(url):
    return urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'techo5-installer'}), timeout=60)


def fetch(url):
    with _open(url) as r:
        return r.read()


def fetch_json(url):
    return json.loads(fetch(url).decode('utf-8'))


def download(url, out):
    partial = out + '.partial'
    with _open(url) as r, open(partial, 'wb') as f:
        shutil.copyfileobj(r, f, 1 << 20)
    os.replace(partial, out)


def download_checked(url, out, sha256):
    """Keeps a download only once its sha256 is the one expected; one already there and right stays."""
    if os.path.exists(out) and hash_file(out) == sha256:
        return
    partial = out + '.partial'
    with _open(url) as r, open(partial, 'wb') as f:
        shutil.copyfileobj(r, f, 1 << 20)
    got = hash_file(partial)
    if got != sha256:
        os.remove(partial)
        fail('%s does not match its checksum (%s, wanted %s)' % (url, got, sha256))
    os.replace(partial, out)


def read_sums(text):
    sums = {}
    for line in text.splitlines():
        m = re.match(r'^([0-9a-f]{64})\s+\*?(\S+)', line)
        if m:
            sums[m.group(2)] = m.group(1)
    return sums


# ------------------------------------------------------------------------------------------ signatures

# Ed25519 verification, written out here because these tools run on whatever Python 3 the machine
# already has, with no pip install step, and the standard library has no ed25519. It is the check from
# RFC 8032 section 5.1.7 and nothing else: decompress the key and R, then compare [S]B against
# R + [h]A. Signing stays in Go (tools/release.ps1); only the maintainer's machine ever needs that.
_P = 2 ** 255 - 19                                            # the field the curve lives in
_L = 2 ** 252 + 27742317777372353535851937790883648493        # the order of the base point
_D = -121665 * pow(121666, _P - 2, _P) % _P                   # the curve constant d
_SQRT_M1 = pow(2, (_P - 1) // 4, _P)                          # a square root of -1, for recovering x


def _recover_x(y, sign):
    """The x that goes with a compressed point's y and sign bit, or None when there is no such point."""
    if y >= _P:
        return None
    x2 = (y * y - 1) * pow(_D * y * y + 1, _P - 2, _P) % _P
    if x2 == 0:
        return None if sign else 0
    x = pow(x2, (_P + 3) // 8, _P)
    if (x * x - x2) % _P != 0:
        x = x * _SQRT_M1 % _P
    if (x * x - x2) % _P != 0:
        return None
    if x & 1 != sign:
        x = _P - x
    return x


def _point_add(p, q):
    """Two points added in extended coordinates (x, y, z, t), which keeps this division-free."""
    a = (p[1] - p[0]) * (q[1] - q[0]) % _P
    b = (p[1] + p[0]) * (q[1] + q[0]) % _P
    c = 2 * p[3] * q[3] * _D % _P
    d = 2 * p[2] * q[2] % _P
    e, f, g, h = b - a, d - c, d + c, b + a
    return (e * f % _P, g * h % _P, f * g % _P, e * h % _P)


def _point_mul(n, p):
    """The point p added to itself n times, by doubling and adding."""
    out = (0, 1, 1, 0)  # the identity
    while n > 0:
        if n & 1:
            out = _point_add(out, p)
        p = _point_add(p, p)
        n >>= 1
    return out


def _point_equal(p, q):
    return (p[0] * q[2] - q[0] * p[2]) % _P == 0 and (p[1] * q[2] - q[1] * p[2]) % _P == 0


def _point_decompress(b):
    """A point back out of its 32 packed bytes, or None when those bytes are not on the curve."""
    if len(b) != 32:
        return None
    n = int.from_bytes(b, 'little')
    sign, y = n >> 255, n & ((1 << 255) - 1)
    x = _recover_x(y, sign)
    return None if x is None else (x, y, 1, x * y % _P)


_G_Y = 4 * pow(5, _P - 2, _P) % _P
_G = (_recover_x(_G_Y, 0), _G_Y, 1, _recover_x(_G_Y, 0) * _G_Y % _P)


def ed25519_verify(public_key, message, signature):
    """True when signature is public_key's ed25519 signature over message, all three as raw bytes."""
    if len(public_key) != 32 or len(signature) != 64:
        return False
    a = _point_decompress(public_key)
    r = _point_decompress(signature[:32])
    if a is None or r is None:
        return False
    s = int.from_bytes(signature[32:], 'little')
    if s >= _L:
        return False
    h = int.from_bytes(hashlib.sha512(signature[:32] + public_key + message).digest(), 'little') % _L
    return _point_equal(_point_mul(s, _G), _point_add(r, _point_mul(h, a)))


def verify_manifest(manifest, signature):
    """Stops unless the release key signed these manifest bytes. The signature file holds base64 of the
    64-byte signature over the manifest exactly as served, which is the form the daemon's updater
    checks, so the two agree byte for byte about what was signed."""
    try:
        key = base64.b64decode(RELEASE_KEY, validate=True)
    except Exception:
        key = b''
    if len(key) != 32:
        fail('no usable release key in this copy of the tools; do not install from it')
    try:
        raw = base64.b64decode(signature.strip(), validate=True)
    except Exception:
        raw = b''
    if len(raw) != 64:
        fail('the release signature is malformed, so the manifest cannot be trusted; stopping')
    if not ed25519_verify(key, manifest, raw):
        fail('the release manifest is NOT signed by the release key: someone between you and GitHub '
             'may have changed it. Nothing has been installed; stopping.')


class Release:
    """A published release: its signed manifest, its SHA256SUMS, and checked downloads of its files."""

    def __init__(self, repo, tag, workdir):
        base = 'https://github.com/%s/releases' % repo
        self.dl = base + ('/latest/download' if tag == 'latest' else '/download/' + tag)
        try:
            raw = fetch(self.dl + '/manifest.json')
        except Exception as e:
            fail('could not read the release manifest from %s: %s' % (self.dl, e))
        try:
            sig = fetch(self.dl + '/manifest.json.sig')
        except Exception as e:
            fail('could not read the release signature from %s/manifest.json.sig: %s; a release without '
                 'its signature is not installed' % (self.dl, e))
        # Checked before the manifest is parsed, the way the daemon's updater does it: until the release
        # key has vouched for them these are bytes off the network and nothing more.
        verify_manifest(raw, sig)
        note('release manifest signed by the project key')
        try:
            self.manifest = json.loads(raw.decode('utf-8'))
        except Exception as e:
            fail('the release manifest from %s is not readable JSON: %s' % (self.dl, e))
        self.version = self.manifest['version']
        try:
            self.sums = read_sums(fetch(self.dl + '/SHA256SUMS').decode('ascii', 'replace'))
        except Exception:
            self.sums = {}
        self.dir = os.path.join(workdir, 'release-%s-%s' % (repo.split('/')[-1], self.version))
        os.makedirs(self.dir, exist_ok=True)

    def rootfs(self, arch):
        entry = (self.manifest.get('rootfs') or {}).get(arch)
        if not entry:
            fail('release %s has no root filesystem for %s' % (self.version, arch))
        out = os.path.join(self.dir, entry['url'].rsplit('/', 1)[-1])
        download_checked(entry['url'], out, entry['sha256'])
        return out

    def asset(self, name):
        if name not in self.sums:
            fail('release %s has no %s in SHA256SUMS; pick another release' % (self.version, name))
        out = os.path.join(self.dir, name)
        download_checked(self.dl + '/' + name, out, self.sums[name])
        return out


def alpine(workdir):
    out = os.path.join(workdir, ALPINE_URL.rsplit('/', 1)[-1])
    download_checked(ALPINE_URL, out, ALPINE_SHA256)
    return out


def _member(tar, name):
    for m in tar.getmembers():
        if m.name.lstrip('./') == name.lstrip('./'):
            return m
    return None


def tar_read(path, name):
    """One file's bytes out of a tarball (compressed or not), or None."""
    with tarfile.open(path, 'r:*') as t:
        m = _member(t, name)
        if m is None or not m.isfile():
            return None
        return t.extractfile(m).read()


def tar_names(path):
    with tarfile.open(path, 'r:*') as t:
        return [m.name for m in t.getmembers()]


def tar_extract_all(path, dest):
    os.makedirs(dest, exist_ok=True)
    with tarfile.open(path, 'r:*') as t:
        if hasattr(tarfile, 'data_filter'):
            t.extractall(dest, filter='data')
        else:
            for m in t.getmembers():
                if m.name.startswith('/') or '..' in m.name.split('/'):
                    fail('unsafe path in %s: %s' % (path, m.name))
            t.extractall(dest)


# ------------------------------------------------------------------------------------------ adb, fastboot

class Adb:
    def __init__(self, serial, exe='adb'):
        self.serial = serial
        self.cmd = tool(exe) + ['-s', serial]

    def run(self, *args):
        return subprocess.run(self.cmd + list(args), stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def state(self):
        r = self.run('get-state')
        return r.stdout.decode('utf-8', 'replace').strip() if r.returncode == 0 else ''

    def sh(self, command):
        r = self.run('shell', command)
        return r.stdout.decode('utf-8', 'replace').replace('\r\n', '\n').strip()

    def push(self, local, remote):
        r = self.run('push', local, remote)
        if r.returncode != 0:
            fail('adb push %s failed: %s' % (local, (r.stderr or r.stdout).decode('utf-8', 'replace').strip()))

    def pull(self, remote, local):
        r = self.run('pull', remote, local)
        return r.returncode == 0

    def exec_out_to_file(self, command, path):
        """A command's raw output into a file, byte for byte."""
        with open(path, 'wb') as f:
            return subprocess.run(self.cmd + ['exec-out', command], stdout=f, stderr=subprocess.DEVNULL).returncode

    def root(self):
        self.run('root')
        time.sleep(3)
        self.run('wait-for-device')

    def reboot(self, target=None):
        self.run(*(['reboot'] + ([target] if target else [])))


class Fastboot:
    def __init__(self, serial, exe='fastboot'):
        self.serial = serial
        self.base = tool(exe)

    def present(self):
        r = subprocess.run(self.base + ['devices'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return any(line.split()[:1] == [self.serial] for line in r.stdout.decode('utf-8', 'replace').splitlines())

    def run(self, *args):
        r = subprocess.run(self.base + ['-s', self.serial] + list(args), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        return r.returncode, r.stdout.decode('utf-8', 'replace').strip()


def wait_for(what, seconds, test, every=5):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if test():
            return
        time.sleep(every)
    fail('timed out after %d s waiting for %s' % (seconds, what))


# ------------------------------------------------------------------------------------------ keys, prompts

def new_api_key():
    return base64.b64encode(secrets.token_bytes(32)).decode('ascii')


def write_private(path, text):
    """Writes a secret to a file only its owner can read. The mode is set as the file is created, so
    there is no moment where it sits on disk readable by everyone else on the machine. Windows mostly
    ignores POSIX modes, where the file simply inherits the folder's permissions as before."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(text)


def valid_api_key(key):
    try:
        return len(base64.b64decode(key, validate=True)) == 32
    except Exception:
        return False


def ask(prompt):
    answer = ''
    while not answer:
        answer = input('   %s: ' % prompt).strip()
    return answer


def wifi_conf(ssid, passphrase):
    """The lines a Dot's wifi.conf holds: the name as hex and WPA's 256-bit key as hex, the key made the
    way wpa_passphrase makes it (PBKDF2-HMAC-SHA1, the name as salt, 4096 rounds). Only the key leaves
    this computer, never the passphrase."""
    s = ssid.encode('utf-8')
    if not 1 <= len(s) <= 32:
        fail('a Wi-Fi network name is 1 to 32 bytes')
    if not 8 <= len(passphrase) <= 63:
        fail('a WPA passphrase is 8 to 63 characters')
    psk = hashlib.pbkdf2_hmac('sha1', passphrase.encode('utf-8'), s, 4096, 32)
    return 'ssid=%s\npsk=%s\n' % (s.hex(), psk.hex())


def ask_wifi(ssid):
    import getpass
    return wifi_conf(ssid, getpass.getpass("   Passphrase for '%s': " % ssid))


# ------------------------------------------------------------------------------------------ serial ports

class SerialPort:
    """A serial port at 115200 8N1, raw, with non-blocking reads."""

    def __init__(self, name):
        self.name = name
        if IS_WINDOWS:
            self._open_windows(name)
        else:
            self._open_posix(name)

    # POSIX (Linux, macOS): termios.
    def _open_posix(self, name):
        import termios
        self.fd = os.open(name, os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
        attrs = termios.tcgetattr(self.fd)
        iflag, oflag, cflag, lflag = attrs[0], attrs[1], attrs[2], attrs[3]
        iflag &= ~(termios.IGNBRK | termios.BRKINT | termios.PARMRK | termios.ISTRIP | termios.INLCR |
                   termios.IGNCR | termios.ICRNL | termios.IXON | termios.IXOFF | termios.IXANY)
        oflag &= ~termios.OPOST
        lflag &= ~(termios.ECHO | termios.ECHONL | termios.ICANON | termios.ISIG | termios.IEXTEN)
        cflag &= ~(termios.CSIZE | termios.PARENB | termios.CSTOPB)
        cflag |= termios.CS8 | termios.CREAD | termios.CLOCAL
        if hasattr(termios, 'CRTSCTS'):
            cflag &= ~termios.CRTSCTS
        attrs[0], attrs[1], attrs[2], attrs[3] = iflag, oflag, cflag, lflag
        attrs[4] = attrs[5] = termios.B115200
        attrs[6][termios.VMIN] = 0
        attrs[6][termios.VTIME] = 0
        termios.tcsetattr(self.fd, termios.TCSANOW, attrs)

    # Windows: kernel32 through ctypes.
    def _open_windows(self, name):
        import ctypes
        from ctypes import wintypes
        k32 = ctypes.WinDLL('kernel32', use_last_error=True)
        self.k32 = k32
        k32.CreateFileW.restype = wintypes.HANDLE
        h = k32.CreateFileW('\\\\.\\' + name, 0x80000000 | 0x40000000, 0, None, 3, 0, None)
        if h is None or h == wintypes.HANDLE(-1).value:
            raise OSError('cannot open %s (error %d)' % (name, ctypes.get_last_error()))
        self.handle = h

        class DCB(ctypes.Structure):
            _fields_ = [('DCBlength', wintypes.DWORD), ('BaudRate', wintypes.DWORD), ('flags', wintypes.DWORD),
                        ('wReserved', wintypes.WORD), ('XonLim', wintypes.WORD), ('XoffLim', wintypes.WORD),
                        ('ByteSize', ctypes.c_ubyte), ('Parity', ctypes.c_ubyte), ('StopBits', ctypes.c_ubyte),
                        ('XonChar', ctypes.c_char), ('XoffChar', ctypes.c_char), ('ErrorChar', ctypes.c_char),
                        ('EofChar', ctypes.c_char), ('EvtChar', ctypes.c_char), ('wReserved1', wintypes.WORD)]

        class TIMEOUTS(ctypes.Structure):
            _fields_ = [('ReadIntervalTimeout', wintypes.DWORD), ('ReadTotalTimeoutMultiplier', wintypes.DWORD),
                        ('ReadTotalTimeoutConstant', wintypes.DWORD), ('WriteTotalTimeoutMultiplier', wintypes.DWORD),
                        ('WriteTotalTimeoutConstant', wintypes.DWORD)]

        dcb = DCB()
        dcb.DCBlength = ctypes.sizeof(DCB)
        if not k32.GetCommState(h, ctypes.byref(dcb)):
            self.close()
            raise OSError('GetCommState failed on %s' % name)
        dcb.BaudRate, dcb.ByteSize, dcb.Parity, dcb.StopBits = 115200, 8, 0, 0
        dcb.flags = 1  # binary; no flow control, DTR and RTS off (as .NET's SerialPort opens it)
        if not k32.SetCommState(h, ctypes.byref(dcb)):
            self.close()
            raise OSError('SetCommState failed on %s' % name)
        t = TIMEOUTS(0xFFFFFFFF, 0, 0, 0, 2000)  # reads return at once with what is there
        k32.SetCommTimeouts(h, ctypes.byref(t))
        self._ctypes, self._wintypes = ctypes, wintypes

    def write(self, data):
        if IS_WINDOWS:
            n = self._wintypes.DWORD()
            self.k32.WriteFile(self.handle, data, len(data), self._ctypes.byref(n), None)
        else:
            view = memoryview(data)
            while view:
                try:
                    view = view[os.write(self.fd, view):]
                except BlockingIOError:
                    time.sleep(0.01)

    def read(self):
        if IS_WINDOWS:
            buf = self._ctypes.create_string_buffer(4096)
            n = self._wintypes.DWORD()
            if not self.k32.ReadFile(self.handle, buf, 4096, self._ctypes.byref(n), None):
                return b''
            return buf.raw[:n.value]
        try:
            return os.read(self.fd, 4096)
        except BlockingIOError:
            return b''

    def close(self):
        if IS_WINDOWS:
            if getattr(self, 'handle', None):
                self.k32.CloseHandle(self.handle)
                self.handle = None
        elif getattr(self, 'fd', None) is not None:
            os.close(self.fd)
            self.fd = None


def list_consoles(ids):
    """The serial ports with these USB ids now present, as (port, USB serial or '')."""
    vid, pid = ids
    found = []
    if IS_WINDOWS:
        import winreg
        present = set()
        try:
            with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, r'HARDWARE\DEVICEMAP\SERIALCOMM') as k:
                i = 0
                while True:
                    try:
                        present.add(winreg.EnumValue(k, i)[1])
                    except OSError:
                        break
                    i += 1
        except OSError:
            return []
        base = r'SYSTEM\CurrentControlSet\Enum\USB'
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, base) as usb:
            i = 0
            while True:
                try:
                    dev = winreg.EnumKey(usb, i)
                except OSError:
                    break
                i += 1
                if not dev.upper().startswith('VID_%s&PID_%s' % (vid.upper(), pid.upper())):
                    continue
                with winreg.OpenKey(usb, dev) as dk:
                    j = 0
                    while True:
                        try:
                            inst = winreg.EnumKey(dk, j)
                        except OSError:
                            break
                        j += 1
                        try:
                            with winreg.OpenKey(dk, inst + r'\Device Parameters') as pk:
                                port = winreg.QueryValueEx(pk, 'PortName')[0]
                        except OSError:
                            continue
                        if port in present:
                            found.append((port, '' if '&' in inst else inst))
    elif IS_MACOS:
        import glob
        for p in sorted(glob.glob('/dev/cu.usbmodem*')):
            found.append((p, p[len('/dev/cu.usbmodem'):]))
    else:
        import glob
        for tty in sorted(glob.glob('/sys/class/tty/ttyACM*')):
            d = os.path.realpath(os.path.join(tty, 'device'))
            for _ in range(3):
                d = os.path.dirname(d)
                try:
                    with open(os.path.join(d, 'idVendor')) as f:
                        v = f.read().strip()
                    with open(os.path.join(d, 'idProduct')) as f:
                        p = f.read().strip()
                except OSError:
                    continue
                if (v, p) == (vid, pid):
                    try:
                        with open(os.path.join(d, 'serial')) as f:
                            s = f.read().strip()
                    except OSError:
                        s = ''
                    found.append(('/dev/' + os.path.basename(tty), s))
                break
    return found


_ESCAPES = re.compile(r'\x1b\[[0-9;?]*[A-Za-z]')


def console_exchange(port, command, wait):
    """Runs one command on a console and returns what it printed, or None when no answer came. The
    markers are printed from variables and matched as whole lines, so the shell echoing the typed line
    (wrapped at 80 columns) never matches one."""
    sp = SerialPort(port)
    try:
        sp.write(b'\n')
        time.sleep(0.4)
        sp.read()
        tag = secrets.token_hex(4)
        begin, end = '__T5BEGIN%s__' % tag, '__T5END%s__' % tag
        sp.write(('b=%s; m=%s; echo $b; %s; echo $m\n' % (begin, end, command)).encode('utf-8'))
        buf = b''
        lines = []
        deadline = time.time() + wait
        while time.time() < deadline:
            chunk = sp.read()
            if chunk:
                buf += chunk
                lines = _ESCAPES.sub('', buf.decode('utf-8', 'replace')).replace('\r', '').split('\n')
                if end in lines:
                    break
            else:
                time.sleep(0.1)
        if end not in lines:
            return None
        out, started = [], False
        for line in lines:
            if started and line == end:
                break
            if started:
                out.append(line)
            elif line == begin:
                started = True
        return '\n'.join(out)
    finally:
        sp.close()


class Console:
    """A unit's USB serial console, found by its serial number: the USB serial where the device reports
    it, else the androidboot.serialno on the kernel command line. Commands only ever run on that unit."""

    def __init__(self, serial, ids):
        self.serial, self.ids, self.port = serial, ids, None

    def _is_unit(self, port):
        try:
            out = console_exchange(port, 'grep -q androidboot.serialno=%s /proc/cmdline && echo IS-THE-UNIT' % self.serial, 3)
        except OSError:
            return False
        return bool(out) and 'IS-THE-UNIT' in out

    def find(self):
        ports = list_consoles(self.ids)
        if self.port and self.port in [p for p, _ in ports]:
            ports.sort(key=lambda ps: ps[0] != self.port)
        for port, usb_serial in ports:
            if usb_serial and usb_serial.upper().startswith(self.serial.upper()):
                self.port = port
                return port
        for port, _ in ports:
            if self._is_unit(port):
                self.port = port
                return port
        return None

    def run(self, command, wait=8):
        sim = os.environ.get('TECHO5_CONSOLE_SIM')
        if sim:  # installer rehearsals: the command goes to a simulated unit
            r = subprocess.run([sys.executable, sim, self.serial, command], stdout=subprocess.PIPE)
            text = r.stdout.decode('utf-8', 'replace').replace('\r\n', '\n')
            if r.returncode != 0:
                return None
            self.port = 'SIM'
            return text.rstrip('\n')
        port = self.port or self.find()
        if not port:
            return None
        guarded = ('if grep -q androidboot.serialno=%s /proc/cmdline; then '
                   'export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; ( %s ); fi'
                   % (self.serial, command))
        try:
            return console_exchange(port, guarded, wait)
        except OSError:
            self.port = None
            return None
