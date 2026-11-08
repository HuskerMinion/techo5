"""Makes the spoken prompts a screenless device (an Echo Dot, an Echo 2nd gen) says while it is set up
over Bluetooth (echod/internal/feature/improv/say). Run it after changing a line below.

Spoken by Piper (https://github.com/OHF-Voice/piper1-gpl) with the LJSpeech voice, whose recordings are
in the public domain (its MODEL_CARD), so the clips are too. Written as raw 16-bit mono PCM at 16 kHz,
which the daemon raises to the speaker's rate.

    python3 -m venv ~/piper-venv && . ~/piper-venv/bin/activate && pip install piper-tts soxr numpy
    # en_US-ljspeech-high.onnx and .onnx.json from huggingface.co/rhasspy/piper-voices, en/en_US/ljspeech/high
    python3 tools/make-dot-prompts.py ~/piper-voices/en_US-ljspeech-high.onnx
"""
import os
import sys

import numpy as np
import soxr
from piper import PiperVoice

# The name of each clip, and what it says. Short and plain: it is heard once, by someone holding a phone.
LINES = {
    'offer': "I'm ready for Wi-Fi. Send it from the Home Assistant app, or from improv wifi dot com.",
    'offer_press': ("I'm ready for Wi-Fi. Send it from the Home Assistant app, or from improv wifi dot com. "
                    "Then press any button on me."),
    'joined_setup': "I'm on Wi-Fi. Add me in Home Assistant, under Devices and services.",
    'failed': "I couldn't join that network. Check the password, and send it again.",
}

RATE = 16000
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'echod', 'internal', 'feature', 'improv', 'say')


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    voice = PiperVoice.load(sys.argv[1])
    os.makedirs(OUT, exist_ok=True)
    for name, text in LINES.items():
        parts, rate = [], None
        for chunk in voice.synthesize(text):
            parts.append(np.frombuffer(chunk.audio_int16_bytes, dtype=np.int16))
            rate = chunk.sample_rate
        audio = np.concatenate(parts).astype(np.float32) / 32768
        audio = soxr.resample(audio, rate, RATE)
        # A quarter second of quiet before it, so it does not run into the tone it follows.
        audio = np.concatenate([np.zeros(RATE // 4, np.float32), audio])
        pcm = (np.clip(audio, -1, 1) * 32767).astype('<i2').tobytes()
        with open(os.path.join(OUT, name + '.pcm'), 'wb') as f:
            f.write(pcm)
        print('%s: %.1f seconds' % (name, len(pcm) / 2 / RATE))


if __name__ == '__main__':
    main()
