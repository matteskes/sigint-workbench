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

## Scholl 2019 — HF transmission-mode classification (§6.6 anchor)

S. Scholl, "Classification of Radio Signals and HF Transmission
Modes with Deep Learning," arXiv:1906.04459 (2019, v1 only),
<https://arxiv.org/abs/1906.04459>. Read 2026-10-04.

Correction over the earlier stub in this file: this is NOT
transient RF fingerprinting — the paper has no fingerprint or
front-end content at all (that material lives in Jagannath 2022).
The SPEC §6.6 framing always cited it correctly; this entry is the
full per-section distill.

### What the paper does

- Skips modulation recognition as an intermediate product: one
  network maps raw IQ windows straight to the *transmission
  mode* — 18 HF classes: Morse (OOK), PSK31, PSK63, QPSK31,
  RTTY 45/170, 50/170, 100/850, Olivia 8/250, 16/500, 16/1000,
  32/1000, DominoEX, MT63-1000, Navtex/Sitor-B, USB, LSB, AM
  broadcast, HF radiofax.
- Deliberately includes near-twin classes (RTTY45 vs RTTY50,
  PSK31 vs PSK63, USB vs LSB) to probe fine-grained resolution.
- Motivation matches §6.6's premise: mode labels are what a SIGINT
  workflow actually wants; modulation-only nets still need a
  hand-crafted second stage (baud rate, pulse shape, bit patterns)
  to name a mode. Classifying the mode directly is the escape.

### Dataset — the reusable part

- Fully synthetic pipeline: plain text (digital modes), speech and
  music of various genres (analog modes), B&W images (fax),
  modulated by standard mode software, then distorted by the
  Watterson HF channel model per CCIR 520 / ITU-R F.520.
- Channel scenarios used: good, moderate, bad, flutter fading,
  Doppler fading — plus clean vectors without fading or Doppler.
- Per-vector impairments on top: AWGN with SNR uniformly spread
  over −10…+25 dB, random frequency offset ±250 Hz, random phase
  offset (for non-coherent reception / receiver mistuning).
- Format: 2048 complex samples @ 6 kHz ≈ 0.34 s (sized for HF's
  mostly <3 kHz channels); 120,000 training + 30,000 validation
  vectors, spread evenly over classes and the SNR grid.
- This is the full Watterson recipe: a concrete, hardware-free
  augmentation stack for B-rung training over our own §11.3
  captures — and the baseline §6.6's GAN question should be scored
  against (GANs only earn their cost if channel-model augmentation
  alone stalls).

### Models and training

- Four CNNs, all ~1.4M params, filter size 3, ReLU + softmax,
  batch norm + dropout: classical CNN (8 layers, dense head),
  all-conv net (stride-2 downsampling, global average pooling
  head), deep CNN (17 layers, VGG-style), residual net (41 layers
  built from eight O'Shea-style 5-layer residual stacks).
- Negative result worth keeping: He-style residual nets, including
  the bottleneck variant, did NOT work well here; only the
  O'Shea-style stack arrangement did. Matters for the B-rung
  architecture shortlist.
- Training: Adam + plateau LR scheduler, batch 128, 30 epochs,
  0.7 h (all-conv) to 5.3 h (residual).

### Results

- Mean accuracy over −10…+25 dB: 85.8% / 90.3% / 93.7% / 94.1%
  (classical / all-conv / deep / residual). Above 90% already at
  −5 dB for the best nets; ~98% above 5 dB.
- Depth buys little: +0.4 pp from 41 layers vs 17, at ~2.8× the
  training time. All-conv is the best accuracy per training hour;
  deep CNN the best per layer count.
- Residual confusions: QPSK/BPSK and RTTY45/RTTY50 (the baud-rate
  twins). USB/LSB separate with high reliability despite being
  spectrally identical bar inversion.

### Caveats

- Sim-to-sim only: training AND validation are synthetic; no
  on-air evaluation anywhere. Robustness is asserted via the
  impairment recipe, not demonstrated — the §11.3/E0 corpus is
  exactly the reality check this paper lacks.
- ±250 Hz CFO covers mistuned narrowband HF, not wide drift; no
  symbol-clock offset is simulated; no unknown/noise class exists
  (§6.6's separate Unknown path is our addition and is needed —
  the real HF band is full of unmodeled traffic).
- Mode set is 2019-era hobby/marine/utility traffic; modern
  proprietary modems (VARA-class) are absent — extending the label
  set is on us.

### What transfers to the workbench

- §6.6 anchors the direct-IQ ambition and the −10…+25 dB
  stratification convention on this paper (SPEC lines already
  accurate). Its 18-mode taxonomy is the natural HF label set,
  aligning with §6.2/§12.5 labeling and the E0 transient-lite
  corpus pass.
- Directly actionable, no SPEC contract yet: Watterson/CCIR-520
  augmentation of our own captures; all-conv or deep CNN as the
  first B-rung architecture; skip vanilla He-ResNet.
- No code or weights released — unlike the repo entry above,
  everything here is reimplementation (fits the PyTorch → ONNX
  pipeline).

## Jagannath et al. 2022 — survey (§6.6/§6.7 context)

A. Jagannath, J. Jagannath, P. S. Pattanshetty Vasanth,
arXiv:2201.00680 — DL-for-RF survey. §IV.C/§V-B6/§V.F feed §6.6's
ladder and §6.7's receiver-entanglement caveat; full recipe
detail lives in SPEC.
