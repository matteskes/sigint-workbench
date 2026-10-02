// Package classify — ONNX model class labels.
//
// ModulationClasses lists the ONNX model's output classes in index
// order. The model (models/train.py) must be exported with exactly
// these labels in this order.
package classify

// ModulationClasses is the model output label set (134-dim input → 5 classes).
var ModulationClasses = []string{
	"am",
	"cw",
	"fm_narrow",
	"fm_wide",
	"noise",
}

// ClassLabel returns the class name for a model output index.
func ClassLabel(i int) string {
	if i < 0 || i >= len(ModulationClasses) {
		return "unknown"
	}
	return ModulationClasses[i]
}

// modulationFromLabel maps a model class to the (modulation, subType)
// display names used by the rule-based classifier.
func modulationFromLabel(label string) (modulation, subType string) {
	switch label {
	case "am":
		return "AM", ""
	case "cw":
		return "CW", ""
	case "fm_narrow":
		return "FM", "NFM"
	case "fm_wide":
		return "FM", "WFM"
	case "noise":
		return "Noise", ""
	default:
		return label, ""
	}
}