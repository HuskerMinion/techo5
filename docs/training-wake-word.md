# Training a wake word of your own

TECHO5 listens with [microWakeWord](https://github.com/kahrendt/microWakeWord) models. If the word you
want isn't in any collection, or the ones there don't hear your voices well, you can train a model
yourself and load it like any other ([A wake word of your own](setup.md#a-wake-word-of-your-own)).

There are two ways. **[wakesmith](https://github.com/HuskerMinion/wakesmith)** does it all from a page
in your browser, and is the one to start with. **By hand**, with microWakeWord's own tools, is further
down: it takes longer, but it's the way for a language other than English, since wakesmith only has
English voices built in.

## With wakesmith

wakesmith trains microWakeWord models on your own computer: nothing is sent anywhere. It mixes
synthetic speech with real voices (takes you record in the browser, the clips your devices kept, and
Common Voice), and scores every model on recordings it never trained on, so you can compare runs
before you pick one. Its [README](https://github.com/HuskerMinion/wakesmith#readme) is the reference;
these are the steps for a TECHO5 device.

### What you need

- **Linux, or WSL2 on Windows** (Ubuntu), Python 3.10 to 3.12.
- **About 16 GB of RAM**, and 50 GB of free disk plus about 20 GB per wake word while it trains.
- **A GPU helps but isn't needed.** NVIDIA and Intel Arc cards run every heavy stage (a full-size
  model in about 15 minutes); a CPU alone works too, more slowly.

### 1. Install it and open the page

Install it as its [Quick start](https://github.com/HuskerMinion/wakesmith#quick-start) says (the wheel
from its Releases page, then `wakesmith setup`, which picks the right build for your GPU and
downloads about 24 GB of training data once), then run `wakesmith serve` and open
`http://localhost:8765/`.

### 2. Start a wake word

Pick **Echo-style or other device** for what it's for, which gives a microWakeWord model, and type
the phrase. *Hear it* says it in a synthetic voice, and the page lists sound-alikes the model must
learn to ignore, and warns about phrases that are too short or everyday words.

### 3. Add real voices

This is optional, and the biggest gain:

- **Record takes** in the browser: close, across the room, with the TV on, quietly. Each person in
  the house saying it a few times helps most. Every fourth take is kept aside for the test.
- **Your device's clips.** Turn on **Keep wake clips** on the device
  ([Wake word clips](setup.md#wake-word-clips)), use it for a while, then copy the clips off (the
  `wake_clips` and `wake_clip_audio` actions, or SSH) into a folder and import it under **Real
  voices**, **Device clips**. wakesmith reads the score and time from the `.json` beside each clip,
  transcribes them, and asks you about the ones only a person can judge. The clips your current
  model got wrong are the best training a new one can have.

### 4. Train, and read the results

Press **Train**. The page shows each stage and the time left. A Quick test takes about 8 minutes with
a GPU; a Standard model about 15. **Results** then shows, for every cutoff, how many of your held-out
takes it caught and how many false triggers it had, run by run, side by side. Pick the cutoff that
catches most of your takes with the false triggers you can live with.

### 5. Export it and load it

Export the **microWakeWord** model: a `.json` and a `.tflite`. Add both on the device's setup page,
**Sound & Voice**, **Wake words of your own**
([A wake word of your own](setup.md#a-wake-word-of-your-own)); the device checks it can run the model
before it keeps it. Pick it as the **Wake word**, set the **Wake word threshold** near the cutoff you
chose, then say it from where you'll use it and watch the setup page's
[wake word scores](setup.md#watching-the-wake-word-scores).

### 6. The next round

Keep **Keep wake clips** on. After a few days, import the new clips and make a **New version** (the
word's menu on the wake words page): it trains again with the new clips added and tells you whether
the new model does better than the one on your device, on the same held-out clips.

## By hand, with microWakeWord's own tools

This is how we trained a two-word English wake word of our own before wakesmith, and the way for
another language. The tools are microWakeWord by Kevin Ahrendt and
[Piper](https://github.com/OHF-Voice/piper1-gpl) with
[piper-sample-generator](https://github.com/rhasspy/piper-sample-generator) by the Rhasspy and Open
Home Foundation projects. Their documentation is the reference; this is the path that worked for
us. Where a step is something we haven't done ourselves (a non-English voice, for one), it says so.

### What you need

- **Linux, or WSL on Windows.** Python 3.10 or later, git and curl.
- **Disk space: about 60 GB** for one model. The negative datasets are about 30 GB unpacked, the
  Python environment about 10 GB, and each set of training features about 10 GB.
- **Time.** A GPU helps with generating samples, but training runs fine on the CPU. Ours ran on a
  32-thread CPU: a first model (12,000 samples, 20,000 steps) took about 15 minutes for the features
  and 35 minutes to train. A larger run with two learning-rate stages took 2 to 14 hours, depending
  on how much data and how many threads it had.

### 1. Install the tools

```sh
mkdir ~/wakeword && cd ~/wakeword
git clone https://github.com/kahrendt/microWakeWord
git -C microWakeWord checkout 4665173cd3
git clone https://github.com/rhasspy/piper-sample-generator
git -C piper-sample-generator checkout 2971426a55
python3 -m venv .venv && . .venv/bin/activate
pip install -e ./microWakeWord
pip install -e ./piper-sample-generator
pip install piper-phonemize-cross==1.2.1 scipy tqdm
```

The two `checkout` lines pin the versions this page was written against (piper-sample-generator
v3.2.0). Newer ones may work too, but their options can change.

For a GPU, install the CUDA builds of PyTorch and TensorFlow first, as their sites describe. Without
them, everything still runs on the CPU.

### 2. Download the background and negative audio

The negative datasets are what the model learns *not* to wake on: speech, a noisy dinner party, and
audio without speech. Room echoes and music and noise are mixed into the wake word samples so they
sound like real rooms.

```sh
mkdir -p negative_datasets
for f in dinner_party.zip dinner_party_eval.zip no_speech.zip speech.zip; do
  curl -sSL -o negative_datasets/$f https://huggingface.co/datasets/kahrendt/microwakeword/resolve/0da95f94302ca2f4aae3b18fc6560fa6d2bba3d1/$f
  unzip -q -o negative_datasets/$f -d negative_datasets
done
```

You also need three folders of 16 kHz mono WAV files for the augmentation:

- `mit_rirs`: room impulse responses, from the
  [MIT environmental impulse responses](https://huggingface.co/datasets/davidscripka/MIT_environmental_impulse_responses).
- `audioset_16k`: everyday sounds, from a part of [AudioSet](https://huggingface.co/datasets/agkphysics/AudioSet).
- `fma_16k`: music, from the [Free Music Archive](https://huggingface.co/datasets/mchl914/fma_xsmall).

microWakeWord's training notebook downloads and converts all three; that's how we got ours.

Some of this audio is licensed for non-commercial use only (the Free Music Archive's tracks, for
one), so a model trained on it is for your own use. Check each dataset's license before sharing one.

### 3. Generate wake word samples

Piper speaks your phrase in thousands of voices. We made 22,000 samples of our phrase with the
multi-speaker English generator, at several speeds and with varied speaker mixes. Here "hey marvin"
stands in for your phrase:

```sh
curl -sSL -o piper-sample-generator/models/en_US-libritts_r-medium.pt \
  https://github.com/rhasspy/piper-sample-generator/releases/download/v2.0.0/en_US-libritts_r-medium.pt
M=piper-sample-generator/models/en_US-libritts_r-medium.pt
python -m piper_sample_generator "hey marvin" --model $M --max-samples 8000 --batch-size 100 \
  --max-speakers 800 --length-scales 0.85 1.0 1.15 --output-dir generated_samples/a
python -m piper_sample_generator "hey, marvin" --model $M --max-samples 4000 --batch-size 100 \
  --max-speakers 800 --length-scales 0.9 1.0 1.2 --output-dir generated_samples/b
python -m piper_sample_generator "hey marvin" --model $M --max-samples 10000 --batch-size 100 \
  --max-speakers 800 --length-scales 0.8 1.0 1.25 --noise-scales 0.5 0.667 0.9 \
  --noise-scale-ws 0.6 0.8 1.0 --slerp-weights 0.3 0.5 0.7 --output-dir generated_samples/c
```

A few things helped:

- **Write the phrase more than one way.** "hey marvin" and "hey, marvin" come out with different
  timing.
- **Vary the speed** (`--length-scales`) and the voice (`--noise-scales`, `--noise-scale-ws`,
  `--slerp-weights`), so the model doesn't learn one way of saying it.
- **Stay under `--max-speakers 800`** with the English generator. Its last speakers had little data
  and sound wrong.
- **Listen to a few dozen samples** before going on. If Piper mispronounces the word, everything
  after it learns the wrong word.

#### Another language

*We haven't trained a non-English model ourselves. This is how the tools support it.*

Use voices of your language, so the samples say the word the way you do. A French speaker's "Alexa"
is not an English speaker's.

- **A multi-speaker generator**, if your language has one. The same release has a French one with 125
  speakers:

  ```sh
  curl -sSL -o piper-sample-generator/models/fr_FR-mls-medium.pt \
    https://github.com/rhasspy/piper-sample-generator/releases/download/v2.0.0/fr_FR-mls-medium.pt
  python -m piper_sample_generator "Alexa" --model piper-sample-generator/models/fr_FR-mls-medium.pt \
    --max-samples 8000 --batch-size 100 --max-speakers 125 --length-scales 0.85 1.0 1.15 \
    --output-dir generated_samples/fr
  ```

  piper-sample-generator's README says mixing speakers (`--slerp-weights`) works for English only.

- **Ordinary Piper voices** from [piper-voices](https://huggingface.co/rhasspy/piper-voices) (each
  voice is a `.onnx` and a `.onnx.json`). Give `--model` several times to cycle through them, for
  example France's `siwis`, `tom` and `upmc` voices. A single-speaker voice gives little variety,
  so mix as many as your language has, and lean on your own recordings (next step).

- **Spell it the way it should sound.** Piper reads the text with the language's own rules
  (espeak-ng). If a borrowed word comes out wrong, write it as it would be spelled in your
  language until it sounds right.

### 4. Add your own voices

Synthetic voices get you most of the way. Real recordings of the people who will use the device close
the gap, and this is the part that most helps an accent the generator doesn't have.

We recorded about 130 takes:

- **With a phone or a laptop next to the device**, so the takes sound close to what the device hears.
- **Across the room:** close, normal and far, facing the device and facing away, quiet and loud, slow
  and quick.
- **As 16 kHz mono 16-bit WAV**, one take per file, with a little silence before and after.
- **Cut into takes by loudness.** We said the word over and over in one long recording, then cut it
  into takes with a short script.

ffmpeg converts other recordings:

```sh
ffmpeg -i take.m4a -ac 1 -ar 16000 -c:a pcm_s16le take.wav
```

**Or record them on the device itself.** The setup page's **Record takes of a word** (see
[Recording takes of a word](setup.md#recording-takes-of-a-word)) records up to two minutes on the
device's own microphones, mixed and leveled the way the wake word hears them, cuts it into takes and
hands them over as a zip of WAVs in this format, one folder per word. No phone, ffmpeg or script.

A hundred takes is small next to thousands of synthetic ones, so they're repeated with different
augmentation each time (step 5) and weighted higher in training (step 6). Keep a few takes out of
training to test with.

**Music the device plays** is worth recording too: a few minutes of the device playing music,
recorded the same way, next to it. Mixed behind the samples it teaches the model to hear you over
its own speaker, and on its own it is a negative set, so music alone doesn't wake it.

**Clips from the device itself** are useful on both sides. Turn on **Keep wake clips** on a device you
already use (see [Wake word clips](setup.md#wake-word-clips)) and it keeps short clips of the wake
word as it really hears it, with the score each one got. The `wake` clips, wakes not taken as a
mistake within 30 seconds, are mostly extra takes of your voices in the room; listen to them too,
since a false wake followed by talk (a TV, say) lands there as well. The `mistake` clips, and the
`noise` and `nearmiss` ones you've listened to and found aren't the word, are the sounds that fool
the model: add them as a negative set so the next model learns to leave them alone. They're 16 kHz
mono 16-bit WAV already, up to four seconds each.

These recordings are your own voices. Keep them on your own machine.

### 5. Make training features

microWakeWord trains on spectrogram features, not on the audio itself. This step mixes room echo,
background sound and level changes into each sample and saves the result. Here it is for the
synthetic samples; the same script with `input_directory` pointing at your takes makes theirs.

```python
import os
from mmap_ninja.ragged import RaggedMmap
from microwakeword.audio.augmentation import Augmentation
from microwakeword.audio.clips import Clips
from microwakeword.audio.spectrograms import SpectrogramGeneration

clips = Clips(input_directory="generated_samples", file_pattern="**/*.wav", max_clip_duration_s=None,
              remove_silence=False, random_split_seed=10, split_count=0.1)
augmenter = Augmentation(augmentation_duration_s=3.2,
    augmentation_probabilities={"SevenBandParametricEQ": 0.1, "TanhDistortion": 0.1, "PitchShift": 0.1,
        "BandStopFilter": 0.1, "AddColorNoise": 0.1, "AddBackgroundNoise": 0.75, "Gain": 1.0, "RIR": 0.5},
    impulse_paths=["mit_rirs"], background_paths=["fma_16k", "audioset_16k"],
    background_min_snr_db=-5, background_max_snr_db=10, min_jitter_s=0.195, max_jitter_s=0.205)

out = "generated_augmented_features"
for split, name, rep, slide in [("training", "train", 2, 10), ("validation", "validation", 1, 10),
                                ("testing", "test", 1, 1)]:
    d = os.path.join(out, split)
    os.makedirs(d, exist_ok=True)
    sg = SpectrogramGeneration(clips=clips, augmenter=augmenter, slide_frames=slide, step_ms=10)
    RaggedMmap.from_generator(out_dir=os.path.join(d, "wakeword_mmap"),
        sample_generator=sg.spectrogram_generator(split=name, repeat=rep), batch_size=100, verbose=True)
```

For your own takes we used:

- `repeat` of 40 for training and 10 for validation;
- a gentler mix (background at 0 to 15 dB below the voice, `RIR` 0.3);
- the device's music recordings added to `background_paths`.

### 6. Train

A YAML file lists the feature sets, each with its weight:

- `truth: true` for wake word sets, `false` for negatives;
- `sampling_weight` for how often a set is drawn;
- `dinner_party_eval` at weight 0, as the set the false accepts are counted on.

This is our first model's:

```yaml
window_step_ms: 10
train_dir: trained_models/my_word
clip_duration_ms: 1500
batch_size: 128
features:
- {features_dir: generated_augmented_features, sampling_weight: 2.0, penalty_weight: 1.0, truth: true, truncation_strategy: truncate_start, type: mmap}
- {features_dir: negative_datasets/speech, sampling_weight: 10.0, penalty_weight: 1.0, truth: false, truncation_strategy: random, type: mmap}
- {features_dir: negative_datasets/dinner_party, sampling_weight: 10.0, penalty_weight: 1.0, truth: false, truncation_strategy: random, type: mmap}
- {features_dir: negative_datasets/no_speech, sampling_weight: 5.0, penalty_weight: 1.0, truth: false, truncation_strategy: random, type: mmap}
- {features_dir: negative_datasets/dinner_party_eval, sampling_weight: 0.0, penalty_weight: 1.0, truth: false, truncation_strategy: split, type: mmap}
training_steps: [20000]
learning_rates: [0.001]
positive_class_weight: [1]
negative_class_weight: [20]
time_mask_max_size: [0]
time_mask_count: [0]
freq_mask_max_size: [0]
freq_mask_count: [0]
eval_step_interval: 500
target_minimization: 0.9
minimization_metric: null
maximization_metric: average_viable_recall
```

Our later models:

- trained in two stages, `training_steps: [30000, 10000]` with `learning_rates: [0.001, 0.0002]` (each
  of the other lists then needs two values too);
- added our own takes at weight 2.0;
- added the device's music as a negative set at weight 5.0.

If a phrase that sounds close to yours keeps waking it, record that phrase and add it as a negative
set.

Then train:

```sh
python -m microwakeword.model_train_eval --training_config=training_parameters.yaml --train 1 --restore_checkpoint 1 \
 --test_tf_nonstreaming 0 --test_tflite_nonstreaming 0 --test_tflite_nonstreaming_quantized 0 --test_tflite_streaming 0 \
 --test_tflite_streaming_quantized 1 --use_weights best_weights \
 mixednet --pointwise_filters "64,64,64,64" --repeat_in_block "1, 1, 1, 1" --mixconv_kernel_sizes "[5], [7,11], [9,15], [23]" \
 --residual_connection "0,0,0,0" --first_conv_filters 32 --first_conv_kernel_size 5 --stride 3
```

### 7. Read the results and pick a threshold

At the end it tests the finished model and prints, for each cutoff, the false rejects (`frr`, the
share of wake words it missed) and the false accepts per hour (`faph`, wakes on the dinner party
recording). The model we use printed:

```
Cutoff 0.98: frr=0.0673; faph=0.000
Cutoff 0.95: frr=0.0541; faph=0.187
Cutoff 0.92: frr=0.0441; faph=0.750
Cutoff 0.90: frr=0.0414; faph=0.937
```

Pick a starting threshold where the false accepts are low enough for you, then test with the takes
you held back. These numbers come from synthetic and recorded test sets, not your rooms, so treat
them as a guide.

On the device, set **Wake word threshold** in Home Assistant, and watch **Last wake score**:

- If real wakes score just under the threshold, lower it a step.
- If false wakes score just over it, raise it.

TECHO5 ignores the `probability_cutoff` in the model's file and uses this setting instead.

### 8. Package it and load it

The model is
`trained_models/my_word/tflite_stream_state_internal_quant/stream_state_internal_quant.tflite`. Copy
it out as `my_word.tflite` and write `my_word.json` beside it:

```json
{
  "type": "micro",
  "wake_word": "My Word",
  "author": "you",
  "website": "",
  "model": "my_word.tflite",
  "trained_languages": ["fr"],
  "version": 2,
  "micro": {
    "probability_cutoff": 0.95,
    "feature_step_size": 10,
    "sliding_window_size": 5,
    "tensor_arena_size": 30000,
    "minimum_esphome_version": "2024.7.0"
  }
}
```

- `wake_word` is the name Home Assistant shows.
- `trained_languages` is the language you trained it in.
- `feature_step_size` matches `window_step_ms` in the training YAML.

Then load it as [A wake word of your own](setup.md#a-wake-word-of-your-own) describes:

1. Put both files in Home Assistant's `custom_wake_words` folder.
2. Restart Home Assistant.
3. Pick the word in the device's **Wake word** list.

## Thanks

- [microWakeWord](https://github.com/kahrendt/microWakeWord) by Kevin Ahrendt: the model, the
  training code and the negative datasets.
- [Piper](https://github.com/OHF-Voice/piper1-gpl) and
  [piper-sample-generator](https://github.com/rhasspy/piper-sample-generator): the voices.
- The [MIT impulse responses](https://huggingface.co/datasets/davidscripka/MIT_environmental_impulse_responses),
  [AudioSet](https://research.google.com/audioset/) and the
  [Free Music Archive](https://github.com/mdeff/fma), for the rooms and the noise.
