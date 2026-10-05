# Research reading notes (non-normative)

Distilled sources behind the §6.6 classifier ladder and the §6.7
emitter-identity probes, plus later reading that may inform them.
Nothing here is contract: no obligations, no D-decisions, no §17.4
slices. SPEC status lines (§6.6, §6.7) always win over this file.

## Repo: Deep-Learning-Based-Radio-Signal-Classification

Source — GitHub, MIT license, default branch `main`:

<https://github.com/alexivaner/Deep-Learning-Based-Radio-Signal-Classification>

"Enhanced Low SNR Radio Signal Classification using Deep Learning" —
Farhan Tandia and Ivan Surya Hutomo, final project for AI Wireless
2020 at National Chiao Tung University (Fall 2020; top final
project). Read 2026-10-04.

### What the repo does

- End-to-end automatic modulation classification (AMC) on raw IQ
  frames — explicitly no handcrafted features, no denoising.
- Dataset: DEEPSIG RADIOML 2018.01A (NEW), 24 modulation classes
  (BPSK/QPSK/8PSK/16PSK/32PSK, OQPSK, GMSK, FM, OOK, 4/8ASK,
  AM-DSB/SSB × SC/WC, 16/64/128/256QAM, 16/32/64/128APSK),
  shipped as HDF5 shards (part0.h5, part1.h5, ...) and unpacked by
  their `extract_dataset.ipynb`.
- Hybrid architecture, split by SNR regime:
  - High SNR — modified ResNet, descending from O'Shea et al. 2018
    (arXiv:1712.04578) via liuzhejun/ResNet-for-Radio-Recognition.
  - Low SNR — Transformer encoder ("Attention Is All You Need").
- Claimed result: at low SNR the hybrid lifts accuracy from under
  20% (their O'Shea-style baseline) to above 70%; remaining errors
  concentrate inside PSK/QAM families and the QAM/APSK ladders.
  This is a course-project benchmark vs their own baseline
  reproduction, not peer-reviewed — quote with care.

### Repo artifacts

- Framework: TensorFlow/Keras, notebook-driven. Weights in tree:
  `resnet_model_mix.h5` and `resnet2_model.h5` (~9.9 MB each),
  `trafo_model.hdf5` / `transformer_model.h5` (~59 MB), plus a TF
  checkpoint pair (`trafo_model.data-00000-of-00001`, `.index`).
- Notebooks under `Submission/`: ResNet training
  (`Classification-proposed-model-resnet-modified-highest.ipynb`),
  Transformer training
  (`Classification-proposed-model-transformer-low.ipynb`),
  evaluation (`Evaluate-Benchmark.ipynb`). `Trial/` holds their
  reproduction of the O'Shea paper. Proposal and final-report PDFs
  are linked from the README.

### Relevance to the workbench

- §6.6 ladder: the O'Shea-style raw-IQ CNN is the canonical
  baseline for B-rung IQ-window models. Their accuracy-vs-SNR
  curves and per-regime confusion matrices are the evaluation
  shape B0/B4 want — stratified by SNR, never single-number
  accuracy. The "CNN collapses low, attention rescues low"
  finding argues against assuming one CNN wins every rung.
- RML2018.01A frames are 1024 IQ samples over an SNR grid of
  -20 to +30 dB (2 dB steps, per the DEEPSIG dataset page). Any
  B-rung comparison against published numbers must match frame
  length and SNR grid, or state the difference.
- §6.7: different task — this classifies the *modulation*, not
  the *emitter*; no transient/fingerprint content. What transfers
  is the raw-IQ training recipe and SNR-stratified evaluation
  discipline, not the model's purpose.
- Framework mismatch: weights are TF/Keras; the workbench
  pipeline is PyTorch → ONNX (see models/README.md — 134-dim
  feature contract today). Reuse means PyTorch reimplementation
  (the likely B-rung move) or tf2onnx conversion; not drop-in.
- Dataset caveat: RML2018.01A is simulated with channel models;
  its numbers never implied real-antenna performance — the same
  caution §6.6 already applies to survey SOTA claims.
- License MIT; authors request attribution on reuse.

## O'Shea, Roy, Clancy 2018 — Over-the-Air DL classification

T. J. O'Shea, T. Roy, T. C. Clancy, "Over-the-Air Deep Learning
Based Radio Signal Classification," IEEE JSTSP 12(1):168–179,
Feb 2018 (arXiv:1712.04578). The ResNet-on-raw-IQ AMC baseline:
2×N IQ in, small residual CNN, per-frame training on RML
datasets; strong at high SNR, degrading sharply below ~0 dB SNR.
Baseline for the repo above; architectural ancestor of any
§6.6 B-rung CNN.

## Scholl 2019 — RF fingerprinting (§6.6 context)

S. Scholl, arXiv:1906.04459 — transient-based RF fingerprinting
with CNNs; documents classifier sensitivity to receiver front-end
differences. Already distilled into SPEC §6.6's research framing;
kept here as the canonical pointer.

## Jagannath et al. 2022 — survey (§6.6/§6.7 context)

A. Jagannath, J. Jagannath, P. S. Pattanshetty Vasanth,
arXiv:2201.00680 — DL-for-RF survey. §IV.C/§V-B6/§V.F feed §6.6's
ladder and §6.7's receiver-entanglement caveat; full recipe
detail lives in SPEC.
