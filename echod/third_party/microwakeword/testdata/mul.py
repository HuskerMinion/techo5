# techo5: makes mul.tflite and mul_tflite.bin, the test model for the MUL kernel (#768). The model is
# made up: no weights are trained and no audio goes into it. It takes a microWakeWord input
# ([1, 3, 40] int8) into three small branches averaged with Keras's Average, as a combined model
# from wakesmith is (ADD, ADD, then MUL by a third), then multiplies the average by one branch and by
# a constant per channel, broadcasting. The .bin is what TensorFlow Lite's reference kernels give for
# 64 inputs, and holds the inputs too.
#
# Run from this directory with TensorFlow 2.x: python3 mul.py

import struct

import numpy as np
import tensorflow as tf

FRAMES, FEATURES, INPUTS, UNITS = 3, 40, 64, 6


class Gain(tf.keras.layers.Layer):
    """Multiplies by a constant per channel: a MUL of [1, n] and [n]."""

    def build(self, shape):
        self.gain = self.add_weight(
            name="gain", shape=(shape[-1],),
            initializer=tf.keras.initializers.Constant([0.5, -1.25, 2.0, 0.75, -0.3, 1.5]),
            trainable=False)

    def call(self, x):
        return x * self.gain


def build():
    x = tf.keras.Input(shape=(FRAMES, FEATURES), batch_size=1)
    flat = tf.keras.layers.Flatten()(x)
    branches = []
    for seed in (1, 2, 3):
        init = tf.keras.initializers.RandomUniform(-0.05, 0.05, seed=seed)
        branches.append(tf.keras.layers.Dense(UNITS, kernel_initializer=init,
                                              bias_initializer=init)(flat))
    avg = tf.keras.layers.Average()(branches)
    both = tf.keras.layers.Multiply()([avg, branches[0]])
    return tf.keras.Model(x, Gain()(both))


def features(rng, n):
    # The range microWakeWord's features take, as int8 after the input's quantization.
    return rng.uniform(0, 26, size=(n, 1, FRAMES, FEATURES)).astype(np.float32)


def main():
    rng = np.random.default_rng(768)
    model = build()
    conv = tf.lite.TFLiteConverter.from_keras_model(model)
    conv.optimizations = [tf.lite.Optimize.DEFAULT]
    conv.representative_dataset = lambda: ([f] for f in features(rng, 200))
    conv.target_spec.supported_ops = [tf.lite.OpsSet.TFLITE_BUILTINS_INT8]
    conv.inference_input_type = tf.int8
    conv.inference_output_type = tf.int8
    flat = conv.convert()
    with open("mul.tflite", "wb") as f:
        f.write(flat)

    interp = tf.lite.Interpreter(
        model_content=flat,
        experimental_op_resolver_type=tf.lite.experimental.OpResolverType.BUILTIN_REF)
    interp.allocate_tensors()
    inp, out = interp.get_input_details()[0], interp.get_output_details()[0]
    scale, zero = inp["quantization"]
    inputs = np.clip(np.round(features(rng, INPUTS) / scale + zero), -128, 127).astype(np.int8)
    outputs = []
    for x in inputs:
        interp.set_tensor(inp["index"], x)
        interp.invoke()
        outputs.append(interp.get_tensor(out["index"]).reshape(-1).copy())

    # uint32 count, uint32 input size, uint32 output size, then each input and its output as int8.
    with open("mul_tflite.bin", "wb") as f:
        f.write(struct.pack("<III", INPUTS, inputs[0].size, outputs[0].size))
        for x, y in zip(inputs, outputs):
            f.write(x.reshape(-1).tobytes())
            f.write(y.astype(np.int8).tobytes())
    ops = sorted({d["op_name"] for d in interp._get_ops_details()})
    print("ops:", ", ".join(ops))


if __name__ == "__main__":
    main()
