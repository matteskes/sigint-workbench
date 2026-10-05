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

## Jagannath et al. 2022 — RF fingerprinting survey (full distill)

A. Jagannath, J. Jagannath, and P. S. Pattanshetty Vasanth Kumar,
"A Comprehensive Survey on Radio Frequency (RF) Fingerprinting:
Traditional Approaches, Deep Learning, and Open Challenges,"
arXiv:2201.00680, v3 (2022-09-06),
<https://arxiv.org/abs/2201.00680>. arXiv Comments: "To appear in
Computer Networks (Elsevier)" — cite as a 2022 survey. Read
2026-10-04 (full pass; 26 content pages).

### What the survey covers

- Encyclopedic map of emitter identification: SIGINT background
  (§II AMC/protocol recognition), applications (§III), ~20 years
  of traditional fingerprinting (§IV), a DL tutorial (§V.A),
  DL-based fingerprinting (§V.B–E), open datasets (§V.F), open
  challenges (§VI). It is the source behind SPEC §6.6/§6.7's
  "survey" citations; this entry is the full per-section distill.
- Section pointers used below match the extraction: §IV = A
  modulation-domain, B statistical, C transient-based, D wavelet,
  E other; §V = B CNN, C GAN, D PNN, E attention, F datasets.

### Traditional approaches (§IV) — the pre-DL toolbox

- Modulation-domain radiometric features: PARADIS (frequency
  error, SYNC correlation, IQ/magnitude/phase error) → SVM error
  0.0034% on 138 identical Atheros NICs; IQ-imbalance
  autocorrelation ≥90% ≥ 15 dB (simulated modulators); spectral
  PCA on 50 RFID smartcards 95→97.5%; 14 weak classifiers +
  weighted voting 88% on 6 WARP boards; constellation-error
  features >95% on 7 TDMA satellite terminals.
- Statistical: RF-DNA (variance/skew/kurtosis, PSD, Gabor) with
  MDA/ML / GRLVQI — 99.7% at 3 classes, ~81% at 7; non-parametric
  ROI features >97% ≥ 10 dB on ZigBee.
- Transient-based (§IV.C — the classical §6.7 feature): turn-on
  transient found by variance threshold, Bayesian step-change, or
  phase-based detection; FFT-Fisher features ≥99.5% on 50
  identical Tmote Sky nodes — robust to distance, multipath, and
  voltage, but broken by antenna polarization change; 8 GSM
  phones 100%; energy-envelope features 99.9% on 7 Bluetooth TXs
  and FLAT from 4 GSps down to 32 MSps.
- Wavelet: DT-CWT preamble features, 80% @ 11 dB SNR, ~7 dB gain
  over equal-count time-domain features; DWFP+WPD+HOS 99% on RFID
  tags; wavelet-Bayes micro-UAV detection 100% ≥ 12 dB.
- Takeaway: near-perfect numbers come from chamber/coax/range
  captures, few classes, handpicked features — the ceiling each
  method hits is deployment tuning, exactly the gap the DL
  section then claims.

### DL approaches (§V) — what actually moved the needle

- Scale record: ORACLE — 99% median on ≤100 COTS WiFi devices,
  96% at 140, 98.6% on 16 bit-similar X310s; 2×conv + 2×FC on raw
  IQ. Proposes injecting controlled TX-side impairments to help
  the classifier; the survey itself objects that this assigns an
  artificial tag rather than reading a true fingerprint.
- The massive study (§V.B.6): DARPA 400 GB, 10,000 devices (5117
  WiFi + 5000 ADS-B), 22 learning tasks. Population scaling is
  graceful; multi-burst joint inference beats single-burst
  decisively; ADS-B (open-air) easier than WiFi; accuracy drops
  hard when channel/environment differs between train and
  validation (Task 3); more training transmissions always help
  (Task 2 — only 2% drop from 501→313 training devices in the
  follow-up); training at low SNR and testing high works, the
  reverse does not (Task 4). Modified 1D AlexNet beat
  ResNet-50-1D on several tasks — deeper is not always better,
  matching Scholl's He-ResNet negative result.
- Channel shift in practice: hovering UAVs, train bursts 1–3 /
  test burst 4 → 50%; ensemble of 12 AlexNet1Ds + multi-tap FIR
  data augmentation → 91–95%, plus 99% open-set detection of
  never-seen UAVs. Cross-domain attention model: 84.3% same-day
  vs 63.8% mixed-day on 10 COTS chipsets — day-shift alone costs
  ~20 pp. Multi-burst aggregation alone reaches ≥95% at
  10k-device scale (§V.B.7 follow-up).
- GAN (§V.C.1): AC-WGAN >95% on 4 UAV types @ 5 dB indoor
  (10–400 m), beats SVM and vanilla AC-GAN; no supervised-CNN
  head-to-head — precisely the evidence SPEC §6.6's B3 assessment
  already cites; nothing new for the GAN question.
- PNN on transients (§V.D): Bayesian ramp detector + low-pass
  energy-spectrum coefficients (K = W/Δf); 90% @ 0 dB, 97.9% @
  25 dB on 8 WiFi devices; accuracy essentially flat from 5 GSps
  down to 28 MSps — transient identity survives heavy decimation.
- Edge-relevant: the authors' own MTL work (§II.C) — radar+comms
  multi-task on raw IQ, 8.4 ms CPU inference, INT8 11.8×
  compression with no significant accuracy loss.

### Open datasets (§V.F) — Table VI

- 86 Bluetooth smartphones (real-world); 17 drone remote
  controllers; 100 aircraft ADS-B (BladeRF) and >140 (USRP B210),
  both 1090 MHz; ORACLE: 16 bit-similar X310s at 2–62 ft plus an
  intentionally-impaired IQ-imbalance variant; 7 hovering DJI
  M100s, ~13k examples × 92k IQ samples (anechoic); POWDER: 4
  bit-similar base stations × WiFi/LTE/5G-NR on 2 days;
  "Exposing the Fingerprint": 20 NI SDRs × 4 setups (wild
  varying-distance, common antenna, coax + 5 dB attenuator,
  anechoic) over 10 days, shipped as raw AND equalized IQ.
- Four of the eight ship SigMF (the rest MATLAB .mat). The 20-SDR
  multi-setup set is the closest public analogue to our §11.3
  corpus plan (multi-day, multi-condition, raw + equalized).

### Challenges (§VI) — absorbed vs new

- Already in SPEC §6.6: the simulation-reality gap as the
  deployment blocker; receiver hardware (IQ imbalance, phase
  noise, clock offsets) etching itself into captures, with
  multi-receiver training and train-on-A/test-on-B as the
  receiver-independence probe.
- Citation correction: SPEC §6.7 credited "§V-B6 Task 3" for
  accuracy collapse when "the receive chain or channel differs".
  Task 3 is the channel/environment half; the cross-receiver
  finding is §VI — a low-end-receiver study showing the same
  transmitter's fingerprint varies across receivers. SPEC pointer
  fixed in the same commit as this entry.
- New, useful for E0/§6.7: transient-based identification is
  reported MORE resilient to impersonation/replay than
  modulation-based; sampling-rate invariance (above); and two
  named open problems — simultaneous multi-emitter
  fingerprinting, and equalization that preserves fingerprints —
  i.e. published SOTA assumes one active emitter per capture,
  which calibrates what our on-air probes can expect.

### Survey caveats

- Heterogeneous evidence: chamber vs coax vs over-the-air, widely
  varying rates/classes/receivers — cross-paper accuracy
  comparisons are indicative only. Breadth over depth (one
  paragraph per work): cite it as a map; the primary papers are
  the evidence.
- Several "open" datasets are synthetic waveforms from real SDR
  chains (ORACLE, POWDER, Exposing-the-Fingerprint) — real
  hardware, not on-air propagation.

### What the workbench can use

- §6.6: citations verified accurate, nothing to absorb; the
  survey independently backs the B2-first instinct ("training the
  neural networks with a larger distribution of data is the key
  to a generalized performance").
- §6.7/E0 design inputs: capture turn-on transients at whatever
  rate §11.3 gives us — published transient features survive
  decimation to 28 MSps, so low-rate attempts are defensible;
  evaluate with day-split train/test (mixed-day costs ~20 pp in
  the literature); try burst aggregation at inference before any
  architecture work; the §VI train-on-receiver-A/test-on-B probe
  maps 1:1 onto the E1 cross-receiver question (still
  hardware-blocked).
- Architecture prior: shallow 1D AlexNet-style stacks repeatedly
  match or beat deep residual nets at scale — aligns the B-rung
  shortlist with Scholl's all-conv/deep-CNN result; skip deep
  residual stacks unless O'Shea-arranged.

## Liao, Liang & Lv 2025 — mixed-signal AMC (FCT, peripheral)

M. Liao, Y. Liang, and P. Lv (Key Lab of Cognition and Decision
Intelligence for Complex Systems, CAS Institute of Automation,
Beijing), "FCT: An Adaptive Model for Classification of Mixed
Radio Signals," Electronics (MDPI) 14(10):2028, 2025-05-16,
<https://doi.org/10.3390/electronics14102028> (CC-BY). Read
2026-10-04 (full text; 11 pages, 28 refs, 0 citations at read
time). Tagged peripheral: AMC, not fingerprinting — logged for
the co-channel-overlap marker and one architecture pattern.

### The task — co-channel mixed-signal AMC

- Automatic MODULATION classification of overlapping co-channel
  signals ("time-frequency aliasing"), emphasizing low SNR. This
  is the O'Shea lineage (what waveform, not which transmitter):
  it does not touch the workbench's core emitter-ID problem.
- Claim: current models degrade at low SNR and "cannot achieve
  good classification results of mixed radio signals"; FCT
  targets exactly that regime.

### The FCT architecture

- Three heads on raw IQ: (1) an FNN gate outputs x ≈ P(high-SNR
  frame) via sigmoid, with the high/low boundary set at 0 dB
  from the empirical crossover; (2) an L-CNN (residual stacks,
  MaxPool2D, SELU dense, dropout) specialized for high SNR,
  weighted by x; (3) an L-Transformer (self-attention block,
  GlobalAveragePooling1D, batch-norm, alpha-dropout, SELU)
  specialized for low SNR, weighted by 1−x. Softmax over the
  blended 24-class distribution; "adaptive" = x is trained
  jointly with everything else (CCE, Adam, batch 1024, ≤1000
  epochs, early stop patience 10).
- The gate learns what a measured-SNR switch would hardcode — a
  sensible pattern, and notably a task-level admission that
  attention helps only below 0 dB here.

### Experiments — RadioML2018.01A

- Subsets of DeepSig RadioML2018.01A: 24 mods, SNR −20…+30 dB in
  2 dB steps, 1.5M examples × 1024 IQ; random 8:2 train/test
  split drawn per mod × SNR. GTX 2080Ti; the paper claims both
  PyTorch 1.12 and tensorflow-gpu 1.15 (unexplained).
- The mixture-construction recipe (their §2, Eq. (1)) did not
  survive text extraction — equation images only — so how two
  signals are superposed, at what power ratio, and how a
  two-signal mixture maps onto a 24-class label space is
  unverified. Nearest kin: their ref [26] (Xu & Lin,
  arXiv:2205.09916), same dataset/task line.

### Results vs baselines

- FCT 84.04% overall, 95.70% peak; CCNN-Atten 57.92% (+26.12 pp
  for FCT), Ti-CNN 65.11% (+18.93 pp). Below 0 dB the L-
  Transformer beats Ti-CNN; above, the CNN family wins — the
  paper presents this crossover as justifying the gate.
- Confusion matrices across all 24 classes: diagonal sharpens
  with SNR, as expected; FCT's is the cleanest of the three.

### Evidence quality — discount the +26 pp

- Synthetic-only: mixtures from RadioML2018.01A waveforms, no
  on-air data anywhere; the authors' own conclusion calls the
  problem "a data fitting problem without actual tests" and
  defers real USRP captures to future work.
- Random split of a dataset with known frame-generation leakage
  inflates absolutes; baseline fairness on a self-defined
  mixture task is unverifiable (26 pp over one baseline
  retrained by the authors); the §2 gap above; rapid-turnaround
  venue, 0 citations at read time.
- Verdict: interesting pattern, weak evidence. Cite the
  pattern, not the numbers.

### Applicability — peripheral, with two keepers

- Overlap marker: multi-emitter co-channel reception is the
  Jagannath §VI open problem; a 2025 modulation-side paper is
  still 100% synthetic superpositions — independently confirms
  the "published SOTA assumes one emitter per capture"
  calibration noted in the Jagannath entry. Relevant when the
  E-ladder goes on-air and §18/§19 waterfalls see real overlap.
- Architecture prior: SNR-gated dual-specialist heads (CNN
  above 0 dB, attention below) — compatible in spirit with the
  B-rung shortlist but NOT shortlist input at this evidence
  level; if ever tried, gate on measured SNR first and skip
  the learned gate.
- Explicitly not core-path: nothing for §6.6 ladder runs, §6.7
  transients, or E0; no SPEC cross-ref changes (§6.6/§6.7
  verified unaffected).
