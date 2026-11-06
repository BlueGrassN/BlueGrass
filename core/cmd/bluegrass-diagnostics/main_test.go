package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"bluegrass/native/core/diagnostics"
)

func TestEmptyInputReportExplicitlySkipsUnmeasuredProbes(t *testing.T) {
	var output bytes.Buffer
	if exit := run(context.Background(), strings.NewReader("{}"), &output); exit != 0 {
		t.Fatal(exit)
	}
	var report diagnostics.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || len(report.Results) != 17 {
		t.Fatal(report)
	}
	for _, r := range report.Results {
		if r.Status != diagnostics.Skipped {
			t.Fatal(r)
		}
	}
}

func TestInvalidInputDoesNotEchoSubmittedValues(t *testing.T) {
	for _, input := range []string{"", "null", "{} {}", `{"password":"must-not-echo"}`, `{"api_base_url":"https://user:must-not-echo@example.com"}`, strings.Repeat(" ", diagnostics.MaxInputBytes+1)} {
		var output bytes.Buffer
		if exit := run(context.Background(), strings.NewReader(input), &output); exit != 2 {
			t.Fatal(exit)
		}
		if output.String() != "{\"version\":1,\"error\":{\"code\":\"invalid_request\"}}\n" {
			t.Fatalf("unsafe error: %q", output.String())
		}
	}
}

func TestCancelledInputReadIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if exit := run(ctx, strings.NewReader("{}"), &output); exit != 2 {
		t.Fatal(exit)
	}
	if strings.Contains(output.String(), "passed") {
		t.Fatal(output.String())
	}
}
