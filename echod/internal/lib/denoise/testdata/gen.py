#!/usr/bin/env python3
"""Makes the test audio in this folder from nothing anyone else owns.

    python3 gen.py        (Linux or WSL; needs espeak-ng, numpy, scipy)

1. speech: espeak-ng says a few requests the way someone would to one of these devices, resampled to
   8 kHz mono with pauses around it (the reference learns the noise from the first 100 ms).
2. noise: pink noise plus a low hum, from a fixed seed, like a fan in the room.
3. in_SNR5.wav, in_SNR15.wav: the two mixed at 5 and 15 dB, speech power over noise power.
4. out_SNR5.wav, out_SNR15.wav: what the reference implementation makes of each, mmse_log_spu.py
   from github.com/vipchengrui/traditional-speech-enhancement (MIT), fetched at a pinned commit and
   run as it is apart from its file names, two NumPy calls newer releases dropped, and its plot.

The WAVs are committed, so this only has to run again if the inputs are meant to change.
"""
import os
import re
import subprocess
import sys
import tempfile
import urllib.request
import wave

import numpy as np
from scipy.signal import lfilter, resample_poly

HERE = os.path.dirname(os.path.abspath(__file__))
RATE = 8000
REF = ('https://raw.githubusercontent.com/vipchengrui/traditional-speech-enhancement/'
       '79cefa66c7a69587f1864a7334cc9da7e31e883d/mmse_log_spu/mmse_log_spu.py')
TEXT = 'Turn on the kitchen lights. Set a timer for ten minutes. What is the weather tomorrow?'


def write(path, x):
    with wave.open(path, 'wb') as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(np.asarray(x, dtype='<i2').tobytes())


def speech(tmp):
    raw = os.path.join(tmp, 'espeak.wav')
    subprocess.run(['espeak-ng', '-v', 'en-us', '-s', '150', '-w', raw, TEXT], check=True)
    with wave.open(raw) as w:
        rate = w.getframerate()
        x = np.frombuffer(w.readframes(w.getnframes()), dtype='<i2').astype(np.float64)
    x = resample_poly(x, RATE, rate)
    pad = np.zeros(RATE // 4)
    x = np.concatenate([pad, x, pad])
    return x / np.max(np.abs(x)) * 0.3 * 32768


def noise(n):
    rng = np.random.default_rng(5)
    # Paul Kellet's pink filter, then a 100 Hz hum a tenth of its level.
    pink = lfilter([0.049922035, -0.095993537, 0.050612699, -0.004408786],
                   [1, -2.494956002, 2.017265875, -0.522189400], rng.standard_normal(n))
    hum = np.sin(2 * np.pi * 100 * np.arange(n) / RATE)
    x = pink / np.std(pink) + 0.1 * hum
    return x / np.std(x)


def reference(tmp, name_in, name_out):
    with urllib.request.urlopen(REF) as r:
        src = r.read().decode()
    src = src.replace("'sp01.wav'", repr(os.path.join(tmp, 'clean.wav')))
    src = src.replace("'in_SNR15_sp01.wav'", repr(os.path.join(HERE, name_in)))
    src = src.replace("'out_SNR15_sp01.wav'", repr(os.path.join(HERE, name_out)))
    src = src.replace('np.fromstring(', 'np.frombuffer(').replace('.tostring()', '.tobytes()')
    src = re.sub(r'(?s)# plot wave.*', '', src).replace('import matplotlib.pyplot as plt', '')
    exec(compile(src, 'mmse_log_spu.py', 'exec'), {'__name__': '__reference__'})


def main():
    with tempfile.TemporaryDirectory() as tmp:
        clean = speech(tmp)
        write(os.path.join(tmp, 'clean.wav'), np.round(clean))
        n = noise(len(clean))
        power = np.mean(clean ** 2)
        for snr in (5, 15):
            mixed = clean + n * np.sqrt(power / 10 ** (snr / 10))
            if np.max(np.abs(mixed)) >= 32767:
                sys.exit('SNR %d clips; lower the speech level' % snr)
            name_in, name_out = 'in_SNR%d.wav' % snr, 'out_SNR%d.wav' % snr
            write(os.path.join(HERE, name_in), np.round(mixed))
            reference(tmp, name_in, name_out)
            print('wrote', name_in, name_out)


if __name__ == '__main__':
    main()
