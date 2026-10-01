// Package classify provides signal classification (rule-based + ML).
package classify

// Result is the output of a classification.
type Result struct {
	Modulation  string  `json:"modulation"`  // FM, AM, FSK, OFDM, etc.
	SubType     string  `json:"subType"`     // WFM, NFM, USB, LSB, etc.
	BandName    string  `json:"bandName"`    // VHF, UHF, etc.
	Source      string  `json:"source"`      // aviation, land_mobile, marine, etc.
	Confidence  float64 `json:"confidence"`  // 0.0 to 1.0
	Method      string  `json:"method"`      // "rules" or "onnx"
	Bandwidth   float64 `json:"bandwidth"`   // Estimated bandwidth in Hz
	Frequency   uint64  `json:"frequency"`   // Center frequency in Hz
}