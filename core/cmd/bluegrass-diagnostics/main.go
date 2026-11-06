// bluegrass-diagnostics reads one JSON request from stdin and writes one JSON
// report to stdout. It never reads profiles, credentials or OS configuration.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"time"

	"bluegrass/native/core/diagnostics"
)

func main() {
	if len(os.Args) != 1 {
		os.Exit(writeError(os.Stdout, "arguments_not_supported", 2))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	exit := run(ctx, os.Stdin, os.Stdout)
	cancel()
	os.Exit(exit)
}

func writeError(out io.Writer, code string, exit int) int {
	// code is always a compile-time constant from this file.
	_, err := io.WriteString(out, "{\"version\":1,\"error\":{\"code\":\""+code+"\"}}\n")
	if err != nil {
		return 1
	}
	return exit
}

func run(ctx context.Context, in io.Reader, out io.Writer) int {
	if ctx.Err() != nil {
		return writeError(out, "input_cancelled", 2)
	}
	type decoded struct {
		req diagnostics.Request
		err error
	}
	ready := make(chan decoded, 1)
	go func() { req, err := diagnostics.DecodeRequest(in); ready <- decoded{req, err} }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var input decoded
	select {
	case <-ctx.Done():
		return writeError(out, "input_cancelled", 2)
	case <-timer.C:
		return writeError(out, "input_timeout", 2)
	case input = <-ready:
	}
	if input.err != nil {
		return writeError(out, "invalid_request", 2)
	}
	report := (diagnostics.Runner{}).Run(ctx, input.req)
	data, err := json.Marshal(report)
	if err != nil || len(data) > 16384 {
		return writeError(out, "report_failed", 1)
	}
	if _, err := out.Write(append(data, '\n')); err != nil {
		return 1
	}
	// Zero means a report was produced, NOT that probes passed.
	return 0
}
