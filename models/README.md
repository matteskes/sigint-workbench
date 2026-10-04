# ML Models

This directory contains ONNX model files for signal classification,
tracked via git-lfs.

## Model Contract

The Go classifier (`internal/classify`) defines the contract the model
must satisfy:

| Name | Tensor name | Shape | Notes |
| --- | --- | --- | --- |
| Input | `features` | `(1, 134)` float32 | one frame per inference |
| Output | `classification` | `(1, 5)` float32 | probability per class |

### Input vector (134 floats, from `SpectralFeatures.ToVector()`)

- `[0]`  Peak frequency as log2(FreqHz / 1kHz). The sweep spans
  500 kHz-1.7 GHz and raw hertz is unlearnable after standardization,
  so Go `ToVector()` applies the same transform before inference.
- `[1]`  Estimated bandwidth (Hz, raw — normalize inside the model)
- `[2]`  Peak power (dB relative to noise floor scale)
- `[3]`  Noise floor (dB)
- `[4]`  SNR (dB)
- `[5]`  Crest factor (peak/RMS of the linear spectrum)
- `[6:134]`  Normalized spectral shape (128 bins, 0.0–1.0)

Because Hz magnitudes are ~1e8 while dB values are ~tens, train.py
bakes an `InputScaler` (per-dimension mean/std) into the ONNX graph —
the Go side sends raw `ToVector()` output and does no preprocessing.
(Spectral entropy is computed by the extractor but is not part of the
134-dim vector; if the model contract changes, `ToVector()` and
`FeatureVectorLength` must be updated together.)

### Output classes (index order, must match `classify.ModulationClasses`)

| Index | Label | Display |
| --- | --- | --- |
| 0 | `am` | AM |
| 1 | `cw` | CW |
| 2 | `fm_narrow` | FM / NFM |
| 3 | `fm_wide` | FM / WFM |
| 4 | `noise` | Noise |

## Training Pipeline (offline, Python)

`models/train.py` generates synthetic IQ per class, extracts the same
134-dim features as the Go extractor, trains a small PyTorch MLP, and
exports ONNX with scalers baked in:

```python
dummy_input = torch.randn(1, 134)
torch.onnx.export(
    model,
    dummy_input,
    "models/classifier.onnx",
    input_names=["features"],
    output_names=["classification"],
    opset_version=14,
)
```

## Running Inference

The Go build tag `onnx` enables ONNX Runtime inference
(`internal/classify/onnx_ort.go`); default builds use the rule
classifier. The runtime library is loaded via `dlopen` — set
`ORT_LIBRARY_PATH` when it is not on the default search path (the
Docker image `docker/Dockerfile.classifier` does this automatically).
For native (non-Docker) builds, `make ort-lib` fetches the same
release into `.ort/` and prints the export line
(`eval "$(make ort-lib)"` before starting the binary).

## Adding a New Model

1. Train and export the `.onnx` file (labels above, in order)
2. Place it in `models/` (`classifier.onnx` by default)
3. Update `config/classifier.yaml` with the new model path
4. Commit with `git add models/ && git commit`
