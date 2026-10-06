package main

import (
	"testing"

	"sigint-workbench/internal/classify"
)

// §16.1: the ONNX model path is flag > env > classifier.yaml, and
// onnx.enabled=false demotes the YAML default (an explicit -model or
// MODEL_PATH still wins — the operator named the model).
func TestResolveClassifierModel(t *testing.T) {
	cases := []struct {
		name        string
		flagModel   string
		envModel    string
		yamlModel   string
		onnxEnabled bool
		want        string
	}{
		{"yaml wins when enabled", "", "", "models/classifier.onnx", true, "models/classifier.onnx"},
		{"flag beats yaml", "/tmp/m.onnx", "", "models/classifier.onnx", true, "/tmp/m.onnx"},
		{"env beats yaml", "", "/tmp/m.onnx", "models/classifier.onnx", true, "/tmp/m.onnx"},
		{"disabled demotes yaml", "", "", "models/classifier.onnx", false, ""},
		{"disabled keeps explicit flag", "/tmp/m.onnx", "", "models/classifier.onnx", false, "/tmp/m.onnx"},
		{"disabled keeps explicit env", "", "/tmp/m.onnx", "models/classifier.onnx", false, "/tmp/m.onnx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveClassifierModel(tc.flagModel, tc.envModel, tc.yamlModel, tc.onnxEnabled)
			if got != tc.want {
				t.Fatalf("resolveClassifierModel = %q, want %q", got, tc.want)
			}
		})
	}
}

// rules.enabled=false with no ONNX model: peaks classify Unknown while
// band identification (geography) and the §6.5 source enum survive.
func TestUnknownClassifier(t *testing.T) {
	res := unknownClassifier{}.Classify(121_500_000, 8000, nil)
	if res.Modulation != "Unknown" || res.Source != "unknown" || res.SubType != "" {
		t.Fatalf("classification = %+v, want Unknown/unknown with no subtype", res)
	}
	if res.BandName == "" || res.BandName == "Unknown" {
		t.Fatalf("band identification should survive rules.enabled=false: got %q", res.BandName)
	}
	if res.Confidence != 0.3 || res.Method != "rules" {
		t.Fatalf("confidence/method = %.2f/%q, want 0.30/rules", res.Confidence, res.Method)
	}
	if res.Frequency != 121_500_000 || res.Bandwidth != 8000 {
		t.Fatalf("frequency/bandwidth = %d/%.0f, want 121500000/8000", res.Frequency, res.Bandwidth)
	}
}

// The rules fallback slot must accept both classifier flavors: the
// rule table and the Unknown placeholder.
func TestRulesFallback(t *testing.T) {
	if _, ok := rulesFallback(classify.NewRuleClassifier()).(*classify.RuleClassifier); !ok {
		t.Fatal("rulesFallback should return the rule classifier when enabled")
	}
	if _, ok := rulesFallback(nil).(unknownClassifier); !ok {
		t.Fatal("rulesFallback(nil) should return the Unknown placeholder")
	}
	var c frameClassifier = &onnxFrameClassifier{rules: rulesFallback(nil)}
	if _, ok := c.(*onnxFrameClassifier); !ok {
		t.Fatal("onnxFrameClassifier should satisfy frameClassifier with the placeholder")
	}
}
