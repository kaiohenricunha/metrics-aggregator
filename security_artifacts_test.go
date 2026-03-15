package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(data)
}

func TestDockerfile_RunsAsNonRoot(t *testing.T) {
	content := mustRead(t, "Dockerfile")

	userDirective := regexp.MustCompile(`(?mi)^\s*USER\s+([^\s#]+)\s*$`)
	matches := userDirective.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatalf("Dockerfile must define a non-root USER")
	}

	finalUser := strings.ToLower(strings.TrimSpace(matches[len(matches)-1][1]))
	if finalUser == "root" || finalUser == "0" || strings.HasPrefix(finalUser, "root:") || strings.HasPrefix(finalUser, "0:") {
		t.Fatalf("Dockerfile must not run final image as root")
	}
}

func TestSidecarManifests_HardenAggregatorSecurityContext(t *testing.T) {
	required := []string{
		"securityContext:",
		"runAsNonRoot: true",
		"readOnlyRootFilesystem: true",
		"allowPrivilegeEscalation: false",
		"capabilities:",
		"drop:",
		"- ALL",
		"seccompProfile:",
		"type: RuntimeDefault",
	}

	paths := []string{
		"test/e2e/manifests/sidecar-pod.yaml",
		"test/e2e/manifests/sidecar-pod-partial.yaml",
		"test/e2e/istio/sidecar-pod-istio.yaml",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			content := mustRead(t, path)
			for _, token := range required {
				if !strings.Contains(content, token) {
					t.Fatalf("%s missing hardening token %q", path, token)
				}
			}
		})
	}
}
