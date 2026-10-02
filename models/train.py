#!/usr/bin/env python3
"""Train the SIGINT Workbench modulation classifier and export it as ONNX.

Pipeline (mirrors the Go pipeline exactly):

  1. Generate synthetic complex-baseband IQ frames per modulation class
     (am, cw, fm_narrow, fm_wide, noise) — 4096 samples @ 4.096 MHz,
     matching config/signal-processor.yaml (fft.size: 4096).
  2. Per frame: complex FFT (unnormalized DFT, one-sided N/2 bins,
     10*log10(|C|^2)) — a line-by-line mirror of
     dsp.ComputeIQFFT (internal/dsp/fft.go).
  3. Feature extraction — a line-by-line mirror of
     classify.ExtractFeatures / ToVector (internal/classify/features.go):
     6 scalars + 128 normalized spectral bins = 134 float32.
  4. Train a small PyTorch MLP (134 -> 64 -> 32 -> 5, softmax).
  5. Export ONNX (opset 14) with a per-dim (x - mean) / std scaler baked
     into the graph, tensor names "features" -> "classification".

Model contract (internal/classify/onnx_ort.go, models/README.md):
  input  features       (1, 134) float32   raw ToVector() output
  output classification (1, 5)   float32   softmax probabilities
  class order: am, cw, fm_narrow, fm_wide, noise

Usage:
  .venv/bin/python models/train.py [--samples-per-class 3000]
                                   [--out models/classifier.onnx]
                                   [--epochs 60] [--lr 0.001] [--seed 42]
"""

from __future__ import annotations

import argparse
import math
import os

import numpy as np
import torch
import torch.nn as nn

# ---------------------------------------------------------------------------
# Go mirror: FFT + spectral feature extraction
# ---------------------------------------------------------------------------

FEATURE_DIM = 134
SPECTRAL_BINS = 128  # classify.FeatureVectorLength
NFFT = 4096          # fft.size in config/signal-processor.yaml
SAMPLE_RATE = 4_096_000  # Hz -> df = 1 kHz/bin, usable band 0..2.048 MHz
DF = SAMPLE_RATE / NFFT
CLASS_LABELS = ["am", "cw", "fm_narrow", "fm_wide", "noise"]

# Complex white noise, per-sample power 1e-6 -> each FFT bin sits at
# ~10*log10(NFFT * 1e-6) dB (same units as the Go pipeline's 10log10(|C|^2)).
NOISE_FLOOR_DB = 10.0 * math.log10(NFFT * 1e-6)  # ~= -23.9 dB


def iq_fft_power_db(iq: np.ndarray, sample_rate: float):
    """Mirror of dsp.ComputeIQFFT (internal/dsp/fft.go).

    Unnormalized complex DFT (same as gonum fourier.NewCmplxFFT), zero
    padded to the next power of two, one-sided spectrum of length nfft/2
    (the Nyquist bin nfft/2 is excluded, exactly like the Go loop
    ``for i := 0; i < half; i++``), power = 10*log10(|C|^2), -300 dB
    floor for zero-magnitude bins.
    """
    n = len(iq)
    nfft = 1
    while nfft < n:
        nfft <<= 1
    spec = np.fft.fft(iq, nfft)  # unnormalized, zero-padded
    half = nfft // 2
    spec = spec[:half]
    freqs = np.arange(half, dtype=np.float64) * (sample_rate / nfft)
    mag = spec.real ** 2 + spec.imag ** 2
    power_db = np.full(half, -300.0, dtype=np.float64)
    nz = mag > 0
    power_db[nz] = 10.0 * np.log10(mag[nz])
    return freqs, power_db


def detect_noise_floor(power_db: np.ndarray) -> float:
    """Mirror of dsp.DetectNoiseFloor: median of the lower 50% of bins."""
    half = len(power_db) // 2
    lower = np.sort(power_db[:half])
    return float(lower[len(lower) // 2])


def estimate_bandwidth_hz(freqs: np.ndarray, power_db: np.ndarray,
                          peak_idx: int) -> float:
    """Mirror of classify.estimateBandwidthHz (-3 dB around the peak bin)."""
    n = len(power_db)
    if n < 3 or not (0 <= peak_idx < n):
        return 0.0
    threshold = power_db[peak_idx] - 3.0
    lo = peak_idx
    while lo > 0 and power_db[lo - 1] >= threshold:
        lo -= 1
    hi = peak_idx
    while hi < n - 1 and power_db[hi + 1] >= threshold:
        hi += 1
    if hi <= lo or len(freqs) <= hi:
        return 0.0
    return float(freqs[hi] - freqs[lo])


def crest_factor(power_db: np.ndarray) -> float:
    """Mirror of classify.crestFactor: peak/RMS of the linear spectrum."""
    lin = np.power(10.0, power_db / 10.0)
    peak = float(lin.max())
    rms = math.sqrt(float(np.sum(lin * lin)) / len(lin))
    if rms <= 0:
        return 0.0
    return peak / rms


def normalize_spectrum(power_db: np.ndarray, noise_floor: float,
                       target: int = SPECTRAL_BINS) -> np.ndarray:
    """Mirror of classify.normalizeSpectrum.

    Integer index arithmetic (srcIdx = i * srcLen / targetLen, Go integer
    division), floor-relative, clipped to 0..60 dB and scaled to 0..1.
    """
    out = np.zeros(target, dtype=np.float32)
    src_len = len(power_db)
    for i in range(target):
        src_idx = i * src_len // target
        if src_idx >= src_len:
            src_idx = src_len - 1
        v = power_db[src_idx] - noise_floor
        if v < 0:
            v = 0.0
        if v > 60:
            v = 60.0
        out[i] = v / 60.0
    return out


def extract_features(freqs: np.ndarray, power_db: np.ndarray,
                     center_hz: float) -> np.ndarray:
    """Mirror of classify.ExtractFeatures + (*SpectralFeatures).ToVector.

    Returns the 134-dim float32 vector the ONNX model consumes:
    [log2(freq kHz), bandwidthHz, peakPowerDB, noiseFloorDB, snrdB,
    crestFactor, spectralShape[128]] — in exactly this order.
    """
    noise_floor = detect_noise_floor(power_db)
    peak_idx = int(np.argmax(power_db))  # Go loop keeps the FIRST max
    peak_power = float(power_db[peak_idx])
    snr = peak_power - noise_floor
    bandwidth = estimate_bandwidth_hz(freqs, power_db, peak_idx)
    crest = crest_factor(power_db)
    shape = normalize_spectrum(power_db, noise_floor)

    vec = np.empty(FEATURE_DIM, dtype=np.float32)
    # Dim 0 is log2(freq kHz), NOT raw hertz — must match Go ToVector
    # (internal/classify/features.go). Raw Hz spans 500k..1.7G and is
    # unlearnable after global standardization.
    f_khz = max(center_hz / 1000.0, 1.0)
    vec[0] = np.float32(math.log2(f_khz))
    vec[1] = np.float32(bandwidth)
    vec[2] = np.float32(peak_power)
    vec[3] = np.float32(noise_floor)
    vec[4] = np.float32(snr)
    vec[5] = np.float32(crest)
    vec[6:6 + SPECTRAL_BINS] = shape
    return vec


# ---------------------------------------------------------------------------
# Synthetic IQ generation (complex baseband, one frame = NFFT samples)
# ---------------------------------------------------------------------------

# Absolute-frequency bands (Hz) per class, mirroring the plausibility rules
# in internal/classify/rules.go. The feature vector carries the peak's
# absolute frequency (frame center + bin offset), so the band must match.
# AM broadcast lives in the MF band (530 kHz - 1.7 MHz); HF voice is
# SSB/CW, so am and cw are frequency-disjoint just like the rules table.
BANDS = {
    "am": ((530_000, 1_700_000),),
    "cw": ((3_000_000, 30_000_000),),
    "fm_narrow": ((146_000_000, 174_000_000), (420_000_000, 512_000_000)),
    "fm_wide": ((118_000_000, 137_000_000), (88_000_000, 108_000_000),
                (156_000_000, 174_000_000)),
    "noise": ((24_000_000, 1_700_000_000),),
}


def _base_noise(rng: np.random.Generator) -> np.ndarray:
    """Complex white Gaussian noise, per-sample power 1e-6."""
    n = NFFT
    s = rng.standard_normal(n) + 1j * rng.standard_normal(n)
    return s * math.sqrt(1e-6 / 2)


def _snr_scale(sig: np.ndarray, rng: np.random.Generator) -> np.ndarray:
    """Rescale so the strongest FFT bin sits U(18, 30) dB ABOVE THE NOISE
    FLOOR BIN (the Go peak detector sees these comfortably)."""
    peak = float(np.abs(np.fft.fft(sig, NFFT)).max())
    if peak <= 0:
        return sig
    cur_db = 20.0 * math.log10(peak)  # |C| in dB units
    target_db = NOISE_FLOOR_DB + rng.uniform(18.0, 30.0)
    return sig * 10.0 ** ((target_db - cur_db) / 20.0)


def _carrier_bin(rng: np.random.Generator, max_bw_bins: int) -> int:
    """Pick a carrier bin leaving the whole signal inside the one-sided
    band (0 .. NFFT/2-1) and away from DC."""
    lo = 250
    hi = NFFT // 2 - 1 - max_bw_bins
    return int(rng.integers(lo, hi))


def _tone(t: np.ndarray, fc: float) -> np.ndarray:
    return np.exp(2j * np.pi * fc * t)


def gen_cw(rng: np.random.Generator) -> tuple[np.ndarray, int]:
    """CW: a narrow carrier line, mostly continuous tone with occasional
    on/off keying (Morse-like). -3 dB width ~0-1 kHz — a single-bin line."""
    n = NFFT
    fc_bin = _carrier_bin(rng, 3)
    fc = fc_bin * DF
    t = np.arange(n) / SAMPLE_RATE
    sig = _tone(t, fc)
    if rng.random() < 0.3:
        # A handful of on/off segments (slow keying, mostly "on")
        segs = int(rng.integers(4, 9))
        bounds = np.sort(rng.integers(1, n - 1, segs - 1))
        bounds = np.concatenate([[0], bounds, [n]])
        env = np.ones(n)
        for i in range(0, segs, 2):
            env[bounds[i]:bounds[i + 1]] = 0.0
        sig = env * sig
    return _snr_scale(sig, rng), fc_bin


def gen_am(rng: np.random.Generator) -> tuple[np.ndarray, int]:
    """AM: strong carrier + symmetric sidebands from voice-like multi-tone
    audio (4-10 sinusoids at 0.5-10 kHz, the real AM broadcast audio band),
    depth 80-100%. Sidebands spread over ~20 source bins, so the -3 dB
    bandwidth walk extends several bins (unlike a pure CW line) and the
    128-bin shape sometimes catches sideband spikes either side of the
    carrier — enough structure to beat CW, especially in the MF band."""
    n = NFFT
    fc_bin = _carrier_bin(rng, 24)
    fc = fc_bin * DF
    t = np.arange(n) / SAMPLE_RATE
    n_tones = int(rng.integers(4, 11))
    audio = np.zeros(n)
    for _ in range(n_tones):
        f_a = rng.uniform(500.0, 10_000.0)
        audio += np.sin(2 * np.pi * f_a * t + rng.uniform(0.0, 2 * np.pi))
    audio /= float(np.abs(audio).max())
    m = rng.uniform(0.8, 1.0)
    sig = (1.0 + m * audio) * _tone(t, fc)
    return _snr_scale(sig, rng), fc_bin


def gen_fm(rng: np.random.Generator, dev_range: tuple[float, float],
           max_bw_bins: int) -> tuple[np.ndarray, int]:
    """FM with a random piecewise-constant deviation that hops across the
    full deviation range — the flat-top ridge a real FM signal with
    voice-like modulation shows. The -3 dB walk then spans the Carson
    width ~2*dev (a few sinusoids would make the instantaneous deviation
    'breathe', carving nulls into the ridge and collapsing the width)."""
    n = NFFT
    fc_bin = _carrier_bin(rng, max_bw_bins)
    fc = fc_bin * DF
    dev = rng.uniform(*dev_range)
    n_hops = int(rng.integers(10, 26))
    t = np.arange(n) / SAMPLE_RATE
    edges = np.linspace(0, n, n_hops + 1).astype(int)
    f_dev = np.empty(n)
    for i in range(n_hops):
        f_dev[edges[i]:edges[i + 1]] = rng.uniform(-dev, dev)
    phase = 2 * np.pi * (fc * t + np.cumsum(f_dev) / SAMPLE_RATE)
    return _snr_scale(np.exp(1j * phase), rng), fc_bin


def gen_fm_narrow(rng: np.random.Generator) -> tuple[np.ndarray, int]:
    """12.5 kHz-style NFM: deviation 3-7 kHz (~6-14 kHz Carson width)."""
    return gen_fm(rng, (3_000.0, 7_000.0), 48)


def gen_fm_wide(rng: np.random.Generator) -> tuple[np.ndarray, int]:
    """WFM: deviation 60-75 kHz (~120-150 kHz Carson width)."""
    return gen_fm(rng, (60_000.0, 75_000.0), 200)


def gen_noise(rng: np.random.Generator) -> tuple[np.ndarray, int]:
    """Noise class: what the peak detector hands the classifier when the
    'signal' is only interference — either a weak local bump 3-12 dB above
    the floor, or elevated flat broadband interference (+6..14 dB)."""
    n = NFFT
    if rng.random() < 0.5:
        fc_bin = _carrier_bin(rng, 5)
        bump_db = rng.uniform(3.0, 12.0)
        amp = 10.0 ** ((NOISE_FLOOR_DB + bump_db) / 20.0) / n  # |C| = amp*n
        t = np.arange(n) / SAMPLE_RATE
        frame = _base_noise(rng) + amp * _tone(t, fc_bin * DF)
        return frame, fc_bin
    gain = rng.uniform(6.0, 14.0)
    return _base_noise(rng) * 10.0 ** (gain / 20.0), 0


CLEAN_GENERATORS = {
    "am": gen_am,
    "cw": gen_cw,
    "fm_narrow": gen_fm_narrow,
    "fm_wide": gen_fm_wide,
}


def build_dataset(rng: np.random.Generator, per_class: int):
    """Build (X, y): X is (per_class * 5, 134) float32, y is int64 labels.

    Frame center frequency is derived so the signal peak lands at a random
    absolute frequency inside the class's plausible band (peakHz =
    FreqHz + offset, exactly like cmd/signal-processor does).
    """
    total = per_class * len(CLASS_LABELS)
    x = np.zeros((total, FEATURE_DIM), dtype=np.float32)
    y = np.zeros(total, dtype=np.int64)
    k = 0
    for ci, label in enumerate(CLASS_LABELS):
        if label == "noise":
            gen = gen_noise
        else:
            gen = CLEAN_GENERATORS[label]
        for _ in range(per_class):
            sig, fc_bin = gen(rng)
            if label != "noise":
                sig = sig + _base_noise(rng)
            bands = BANDS[label]
            lo, hi = bands[int(rng.integers(0, len(bands)))]
            # The SDR tunes the frame CENTER into the band and the signal
            # appears at center + offset inside the frame (offset <= 2 MHz
            # for a 4096-sample @ 4.096 MHz frame). The Go pipeline feeds
            # the extractor the peak's ABSOLUTE frequency (peakHz =
            # frame.FreqHz + offset), so that is what we pass.
            center_hz = rng.uniform(lo, hi)
            peak_abs_hz = center_hz + fc_bin * DF
            freqs, power_db = iq_fft_power_db(sig, SAMPLE_RATE)
            x[k] = extract_features(freqs, power_db, peak_abs_hz)
            y[k] = ci
            k += 1
    return x, y


# ---------------------------------------------------------------------------
# Model, training, ONNX export
# ---------------------------------------------------------------------------

INPUT_NAME = "features"
OUTPUT_NAME = "classification"
OPSET = 14


class Classifier(nn.Module):
    """Baked-in per-dim standardization + 134 -> 64 -> 32 -> 5 MLP (logits).

    The scaler is a registered buffer, so it travels with the weights into
    the ONNX graph — the Go side sends raw ToVector() output and does no
    preprocessing. Training and export use this exact module, so the
    exported graph is numerically what was trained.
    """

    def __init__(self, mean: np.ndarray, std: np.ndarray):
        super().__init__()
        self.register_buffer("mean", torch.as_tensor(mean, dtype=torch.float32))
        self.register_buffer("std", torch.as_tensor(std, dtype=torch.float32))
        self.net = nn.Sequential(
            nn.Linear(FEATURE_DIM, 64),
            nn.ReLU(),
            nn.Linear(64, 32),
            nn.ReLU(),
            nn.Linear(32, len(CLASS_LABELS)),
        )

    def forward(self, x):
        x = (x - self.mean) / self.std
        return self.net(x)


class ExportModel(nn.Module):
    """ONNX wrapper: the trained Classifier plus a final softmax, so the
    output tensor is class probabilities (see onnx_ort.go)."""

    def __init__(self, classifier: Classifier):
        super().__init__()
        self.classifier = classifier

    def forward(self, x):
        return torch.softmax(self.classifier(x), dim=-1)


def train_and_export(x, y, out_path: str, epochs: int, lr: float,
                     seed: int):
    """Split, train, report, export. Returns (torch_acc, ort_acc)."""
    n = len(y)
    # The dataset is ordered by class — shuffle before the split so every
    # class appears in both train and test (stratification at this scale
    # is not worth the extra code).
    perm = np.random.default_rng(seed).permutation(n)
    cut = int(n * 0.8)
    x_tr, x_te = x[perm[:cut]], x[perm[cut:]]
    y_tr, y_te = y[perm[:cut]], y[perm[cut:]]

    # Per-dim scaler fit on the TRAIN split only (baked into the graph).
    mean = x_tr.mean(axis=0)
    std = np.clip(x_tr.std(axis=0), 1e-8, None)

    torch.manual_seed(seed)
    cls = Classifier(mean, std)
    opt = torch.optim.Adam(cls.parameters(), lr=lr)
    loss_fn = nn.CrossEntropyLoss()

    xt = torch.from_numpy(x_tr.astype(np.float32))
    yt = torch.from_numpy(y_tr)
    n_tr = xt.shape[0]
    for epoch in range(epochs):
        perm = torch.randperm(n_tr)
        total = 0.0
        for i in range(0, n_tr, 128):
            idx = perm[i:i + 128]
            opt.zero_grad()
            loss = loss_fn(cls(xt[idx]), yt[idx])
            loss.backward()
            opt.step()
            total += float(loss.detach()) * len(idx)
        if (epoch + 1) % 10 == 0 or epoch == 0:
            print(f"  epoch {epoch + 1:3d}/{epochs}  loss {total / n_tr:.4f}")

    # Torch-side accuracy
    cls.eval()
    with torch.no_grad():
        pred_tr = cls(torch.from_numpy(x_tr.astype(np.float32))).argmax(1).numpy()
        pred_te = cls(torch.from_numpy(x_te.astype(np.float32))).argmax(1).numpy()
    acc_tr = float((pred_tr == y_tr).mean())
    acc_te = float((pred_te == y_te).mean())
    print(f"  accuracy  train {acc_tr:.4f}  test {acc_te:.4f}")
    for ci, label in enumerate(CLASS_LABELS):
        m = y_te == ci
        if m.any():
            print(f"    {label:<10s} {(pred_te[m] == ci).mean():.4f}")

    # Export ONNX with the scaler baked in.
    wrapper = ExportModel(cls).eval()
    dummy = torch.zeros(1, FEATURE_DIM)
    torch.onnx.export(
        wrapper,
        (dummy,),
        out_path,
        input_names=[INPUT_NAME],
        output_names=[OUTPUT_NAME],
        opset_version=OPSET,
        dynamic_axes={INPUT_NAME: {0: "batch"}, OUTPUT_NAME: {0: "batch"}},
    )
    size_kb = os.path.getsize(out_path) / 1024.0
    print(f"  exported {out_path} ({size_kb:.1f} KB, opset {OPSET})")

    # Verify the exported graph with ONNX Runtime itself.
    import onnxruntime as ort

    sess = ort.InferenceSession(out_path)
    logits_or_none = sess.run([OUTPUT_NAME], {INPUT_NAME: x_te.astype(np.float32)})[0]
    assert logits_or_none.shape == (len(x_te), len(CLASS_LABELS)), \
        f"ORT output shape {logits_or_none.shape}"
    probs = logits_or_none
    assert float(probs.min()) >= 0.0 and float(probs.max()) <= 1.0
    assert np.allclose(probs.sum(axis=1), 1.0, atol=1e-4), "rows must be softmax"
    acc_ort = float((probs.argmax(1) == y_te).mean())
    print(f"  ONNX Runtime test accuracy {acc_ort:.4f} "
          f"(torch {acc_te:.4f}, |diff| {abs(acc_ort - acc_te):.4f})")
    assert acc_ort >= acc_te - 0.01, "ORT and torch accuracy diverge"
    return acc_te, acc_ort


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(description="Train + export the ONNX classifier")
    ap.add_argument("--samples-per-class", type=int, default=3000)
    ap.add_argument("--out", default=os.path.join(here, "classifier.onnx"))
    ap.add_argument("--epochs", type=int, default=60)
    ap.add_argument("--lr", type=float, default=1e-3)
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()

    rng = np.random.default_rng(args.seed)
    print(f"generating {args.samples_per_class} frames x {len(CLASS_LABELS)} classes "
          f"({NFFT} samples @ {SAMPLE_RATE / 1e6:.3f} MHz)")
    x, y = build_dataset(rng, args.samples_per_class)
    print(f"features: {x.shape} (float32), label balance: "
          f"{[int((y == i).sum()) for i in range(len(CLASS_LABELS))]}")
    for i, name in enumerate(
            ["log2freqkHz", "bandwidthHz", "peakPowerDB", "noiseFloorDB", "snrdB", "crestFactor"]):
        print(f"  [{i}] {name:<14s} mean {x[:, i].mean():.6g}  std {x[:, i].std():.6g}")

    print(f"training {args.epochs} epochs (lr {args.lr})")
    train_and_export(x, y, args.out, args.epochs, args.lr, args.seed)
    print("done")


if __name__ == "__main__":
    main()
