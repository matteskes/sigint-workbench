# ML Models

This directory contains ONNX model files for signal classification,
tracked via git-lfs.

## Training Pipeline (offline, Python)

Models are trained externally using PyTorch and exported to ONNX:

```python
import torch
import torch.onnx

# Load your trained model
model = torch.load("signal_classifier.pth")
model.eval()

# Export to ONNX
dummy_input = torch.randn(1, 134)  # 6 scalar features + 128 spectral bins
torch.onnx.export(
    model,
    dummy_input,
    "models/classifier.onnx",
    input_names=["features"],
    output_names=["classification"],
    opset_version=14,
)
```

## Model Input Format

The model expects a 134-element float32 vector:
- [0]   Center frequency (Hz)
- [1]   Estimated bandwidth (Hz)
- [2]   Peak power (dB)
- [3]   Noise floor (dB)
- [4]   SNR (dB)
- [5]   Crest factor
- [6:134]  Normalized spectral shape (128 bins, 0.0–1.0)

## Model Output Format

The model outputs a probability distribution over signal classes:
- aviation, land_mobile, marine, broadcast, amateur, gnss, wifi, radar, unknown

## Adding a New Model

1. Train and export the `.onnx` file
2. Place it in `models/`
3. Update `config/classifier.yaml` with the new model path
4. Commit with `git add models/ && git commit`