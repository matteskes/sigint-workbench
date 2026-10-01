// classifier — Signal classification (rule-based + ONNX ML).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/classify"
)

func main() {
	modelPath := flag.String("model", "models/classifier.onnx", "ONNX model path")
	flag.Parse()

	// Rule-based classifier (always available)
	rules := classify.NewRuleClassifier()
	_ = rules

	// ONNX classifier (if model exists)
	onnx := classify.NewONNXClassifier(*modelPath)
	if err := onnx.Load(); err != nil {
		fmt.Printf("classifier: ONNX model not loaded (%v), using rules only\n", err)
	} else {
		fmt.Println("classifier: ONNX model loaded")
	}

	fmt.Println("classifier: starting")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("classifier: stopped")
}