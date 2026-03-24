package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIInvalidOptions(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string // Expected substring in the error message
	}{
		{
			name:     "cert-file missing key-file",
			args:     []string{"--cert-file=cert.pem"},
			expected: "if any flags in the group [cert-file key-file] are set they must all be set",
		},
		{
			name:     "key-file missing cert-file",
			args:     []string{"--key-file=key.pem"},
			expected: "if any flags in the group [cert-file key-file] are set they must all be set",
		},
		{
			name:     "mutually exclusive noauth and controller",
			args:     []string{"--noauth=ThisIsInsecure", "--controller=http://localhost"},
			expected: "if any flags in the group [noauth controller] are set none of the others can be",
		},
		{
			name:     "http mode missing auth configuration",
			args:     []string{"--http=:8080"},
			expected: "http mode requires either --controller or --noauth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			
			// Capture output so we don't spam stdout during tests
			var outBuf bytes.Buffer
			cmd.SetOut(&outBuf)
			cmd.SetErr(&outBuf)
			
			// We provide specific arguments
			cmd.SetArgs(tt.args)
			
			// Run the command and expect an error
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected command to fail, but it succeeded")
			}
			
			if !strings.Contains(err.Error(), tt.expected) {
				t.Errorf("expected error to contain %q, got: %q", tt.expected, err.Error())
			}
		})
	}
}
