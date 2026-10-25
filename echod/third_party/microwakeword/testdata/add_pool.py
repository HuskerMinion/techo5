# techo5: makes add_pool.tflite and add_pool_tflite.bin, the test model for the ADD, AVERAGE_POOL_2D
# and MAX_POOL_2D kernels. The model is made up: no weights are trained and no audio goes into it.
# It takes a microWakeWord input ([1, 3, 40] int8) and runs it through both pools, with SAME and
# VALID padding, a broadcasting ADD with a fused RELU and an ADD with a constant per channel. The
# .bin is what TensorFlow Lite's reference kernels give for 64 inputs, and holds the inputs too.
#
# Run from this directory with TensorFlow 2.x: python3 add_pool.py

import struct

import numpy as np
import tensorflow as tf

FRAMES, FEATURES, INPUTS = 3, 40, 64


class Offset(tf.keras.layers.Layer):
    """Adds a constant per channel: an ADD of [1, h, w, c] and [c]."""

    def build(self, shape):
        self.offset = self.add_weight(
            name="offset", shape=(shape[-1],),
            initializer=tf.keras.initializers.Constant([0.75, -1.5, 2.25, -0.5, 1.0]),
            trainable=False)

    def call(self, x):
        return x + self.offset


class Spread(tf.keras.layers.Layer):
    """Adds [1, 1, w, c] to [1, h, w, c], broadcasting over height, then RELU."""

    def call(self, a, b):
        return tf.nn.relu(tf.add(a, b))


def build():
    x = tf.keras.Input(shape=(FRAMES, FEATURES), batch_size=1)
    r = tf.keras.layers.Reshape((FRAMES, 8, 5))(x)
    same = tf.keras.layers.AveragePooling2D((3, 4), strides=(1, 2), padding="same")(r)   # [1, 3, 4, 5], padded on all sides
    avg = tf.keras.layers.AveragePooling2D((3, 2), strides=(1, 2), padding="valid")(r)   # [1, 1, 4, 5]
    top = tf.keras.layers.MaxPooling2D((3, 2), strides=(1, 2), padding="valid")(r)       # [1, 1, 4, 5]
    both = tf.keras.layers.Add()([avg, top])
    spread = Spread()(both, same)                                                         # broadcast over height
    out = Offset()(spread)
    return tf.keras.Model(x, tf.keras.layers.Flatten()(out))


def features(rng, n):
    # The range microWakeWord's features take, as int8 after the input's quantization.
    return rng.uniform(0, 26, size=(n, 1, FRAMES, FEATURES)).astype(np.float32)


def main():
    rng = np.random.default_rng(190)
    model = build()
    conv = tf.lite.TFLiteConverter.from_keras_model(model)
    conv.optimizations = [tf.lite.Optimize.DEFAULT]
    conv.representative_dataset = lambda: ([f] for f in features(rng, 200))
    conv.target_spec.supported_ops = [tf.lite.OpsSet.TFLITE_BUILTINS_INT8]
    conv.inference_input_type = tf.int8
    conv.inference_output_type = tf.int8
    flat = conv.convert()
    with open("add_pool.tflite", "wb") as f:
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
    with open("add_pool_tflite.bin", "wb") as f:
        f.write(struct.pack("<III", INPUTS, inputs[0].size, outputs[0].size))
        for x, y in zip(inputs, outputs):
            f.write(x.reshape(-1).tobytes())
            f.write(y.astype(np.int8).tobytes())
    ops = sorted({d["op_name"] for d in interp._get_ops_details()})
    print("ops:", ", ".join(ops))


if __name__ == "__main__":
    main()
