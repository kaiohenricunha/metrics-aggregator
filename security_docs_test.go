package main

import (
	"os"
	"strings"
	"testing"
)

func TestSecurityPolicyDocumentExistsWithRequiredSections(t *testing.T) {
	data, err := os.ReadFile("SECURITY.md")
	if err != nil {
		t.Fatalf("SECURITY.md must exist: %v", err)
	}
	content := string(data)

	required := []string{
		"# Security Policy",
		"## Reporting a Vulnerability",
		"## Supported Versions",
		"## Disclosure Process",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			t.Fatalf("SECURITY.md missing section %q", token)
		}
	}
}

func TestREADME_DocumentsSecurityRelevantConfiguration(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("README.md must exist: %v", err)
	}
	content := string(data)

	required := []string{
		"`METRICS_AGGREGATOR_PORT`",
		"`METRICS_SECURITY_MODE`",
		"`METRICS_CACHE_TTL`",
		"`METRICS_MAX_INFLIGHT`",
		"`METRICS_SERVER_READ_HEADER_TIMEOUT`",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			t.Fatalf("README.md missing security-relevant config token %q", token)
		}
	}
}
