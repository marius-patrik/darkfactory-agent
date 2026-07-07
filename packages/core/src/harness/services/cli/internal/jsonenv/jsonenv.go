// Package jsonenv implements the --json output envelope defined in D3 §C3.
//
// Every rommie command that supports --json emits either a single JSON object
// (get/status commands) or newline-delimited JSON objects (list/stream commands).
// All envelopes carry a schema_version field so CI scripts can depend on stability.
package jsonenv

import (
	"encoding/json"
	"fmt"
	"os"
)

// SchemaVersion is the stable envelope version (D3 §C3).
const SchemaVersion = "1"

// Envelope is the top-level wrapper for all --json command output.
type Envelope struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	// Data holds command-specific payload when OK is true.
	Data any `json:"data,omitempty"`
	// Error holds a human-readable error message when OK is false.
	Error string `json:"error,omitempty"`
	// ExitCode mirrors the process exit code (D3 §C3 exit-code contract).
	ExitCode int `json:"exit_code"`
}

// Print writes a success envelope to stdout and returns nil.
func Print(data any) error {
	return printEnvelope(Envelope{
		SchemaVersion: SchemaVersion,
		OK:            true,
		Data:          data,
		ExitCode:      0,
	})
}

// PrintError writes a failure envelope to stdout (callers still exit with code).
func PrintError(msg string, exitCode int) error {
	return printEnvelope(Envelope{
		SchemaVersion: SchemaVersion,
		OK:            false,
		Error:         msg,
		ExitCode:      exitCode,
	})
}

func printEnvelope(e Envelope) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return fmt.Errorf("jsonenv: encode: %w", err)
	}
	return nil
}
