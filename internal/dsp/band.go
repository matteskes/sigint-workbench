// Package dsp — frequency band identification.
package dsp

// Band represents a known frequency band.
type Band struct {
	Name      string  `json:"name"`
	FreqMin   uint64  `json:"freqMin"` // Hz
	FreqMax   uint64  `json:"freqMax"` // Hz
	Category  string  `json:"category"`
	Notes     string  `json:"notes,omitempty"`
}

// KnownBands is a table of common RF bands.
var KnownBands = []Band{
	{Name: "LF", FreqMin: 30_000, FreqMax: 300_000, Category: "low_frequency", Notes: "Navigation, time signals"},
	{Name: "MF", FreqMin: 300_000, FreqMax: 3_000_000, Category: "medium_frequency", Notes: "AM broadcast (530-1700 kHz), maritime"},
	{Name: "HF", FreqMin: 3_000_000, FreqMax: 30_000_000, Category: "high_frequency", Notes: "Shortwave, amateur, maritime, aviation"},
	{Name: "VHF-Low", FreqMin: 30_000_000, FreqMax: 50_000_000, Category: "vhf", Notes: "FM broadcast (88-108 MHz in some regions), amateur"},
	{Name: "VHF-Mid", FreqMin: 50_000_000, FreqMax: 174_000_000, Category: "vhf", Notes: "TV, aviation (118-137), marine (156-174), amateur (144-148)"},
	{Name: "UHF", FreqMin: 174_000_000, FreqMax: 1_000_000_000, Category: "uhf", Notes: "TV, land mobile (450-470, 800-870), amateur (420-450, 1240-1300)"},
	{Name: "L-Band", FreqMin: 1_000_000_000, FreqMax: 2_000_000_000, Category: "microwave", Notes: "GPS (1575), Wi-Fi (2.4 GHz edge), radar"},
	{Name: "S-Band", FreqMin: 2_000_000_000, FreqMax: 4_000_000_000, Category: "microwave", Notes: "Wi-Fi (5 GHz), radar, satellite"},
	{Name: "C-Band", FreqMin: 4_000_000_000, FreqMax: 8_000_000_000, Category: "microwave", Notes: "Satellite comms, radar"},
}

// IdentifyBand finds the known band for a given frequency.
func IdentifyBand(hz uint64) *Band {
	for i := range KnownBands {
		if hz >= KnownBands[i].FreqMin && hz < KnownBands[i].FreqMax {
			return &KnownBands[i]
		}
	}
	return nil
}

// BandName returns the band name for a frequency, or "Unknown".
func BandName(hz uint64) string {
	b := IdentifyBand(hz)
	if b == nil {
		return "Unknown"
	}
	return b.Name
}