package main

import (
	"testing"
	"time"
)

func TestLoadtestRejectsUnsafeOrUnboundedArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"-uri", "mongodb://localhost", "-duration", "0s"}, {"-uri", "mongodb://localhost", "-concurrency", "0"}, {"-uri", "mongodb://localhost", "-max-p95-ms", "NaN"}, {"-uri", "mongodb://localhost", "-max-error-rate", "2"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
}

func TestLatencyHistogramMergesAndBounds(t *testing.T) {
	var first, second histogram
	for range 95 {
		first.add(time.Millisecond, nil)
	}
	for range 5 {
		second.add(10*time.Millisecond, nil)
	}
	first.merge(second)
	if first.Count != 100 || first.quantile(.95) < 1 || first.quantile(.95) > 1.023 || first.quantile(.99) < 10 || first.quantile(.99) > 10.23 {
		t.Fatalf("invalid histogram: %+v", first.summary())
	}
}
