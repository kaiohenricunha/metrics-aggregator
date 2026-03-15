package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func mustReadText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestDockerPublish_TriggersOnMainAndSemverTags(t *testing.T) {
	content := mustReadText(t, ".github/workflows/docker-publish.yml")

	branchesRe := regexp.MustCompile(`(?ms)push:\s*.*branches:\s*\[\s*main\s*\]`)
	if !branchesRe.MatchString(content) {
		t.Fatal("docker-publish must trigger on pushes to main")
	}

	tagsRe := regexp.MustCompile(`tags:\s*\[(?:'v\*\.\*\.\*'|"v\*\.\*\.\*")\]`)
	if !tagsRe.MatchString(content) {
		t.Fatal("docker-publish must trigger on semantic version tags")
	}
}

func TestReleaseWorkflow_ChainsFromDockerPublish(t *testing.T) {
	content := mustReadText(t, ".github/workflows/release.yml")

	if !strings.Contains(content, "workflow_run:") {
		t.Fatal("release.yml must trigger from workflow_run")
	}
	if !strings.Contains(content, `workflows: ["Docker publish"]`) {
		t.Fatal(`release.yml must chain from "Docker publish" workflow`)
	}
	if !strings.Contains(content, "types: [completed]") {
		t.Fatal("release.yml must wait for completed Docker publish runs")
	}
	if regexp.MustCompile(`(?m)^\s*push:\s*$`).MatchString(content) {
		t.Fatal("release.yml must not trigger directly on push events")
	}
}

func TestReleaseWorkflow_DoesNotUseCrossWorkflowNeeds(t *testing.T) {
	content := mustReadText(t, ".github/workflows/release.yml")
	if strings.Contains(content, "needs: scan-image") {
		t.Fatal("release.yml must not use cross-workflow needs dependency")
	}
}

func TestReleaseWorkflow_RequiresTaggedSuccessfulDockerRun(t *testing.T) {
	content := mustReadText(t, ".github/workflows/release.yml")
	required := []string{
		"github.event.workflow_run.conclusion == 'success'",
		"github.event.workflow_run.head_branch != 'main'",
		"github.event.workflow_run.head_sha",
		"git tag --points-at",
		"ref: refs/tags/${{ needs.resolve-tag.outputs.tag }}",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			t.Fatalf("release.yml missing required tag gate token %q", token)
		}
	}
}

func TestDockerPublish_ReferencesValidTrivyStepID(t *testing.T) {
	content := mustReadText(t, ".github/workflows/docker-publish.yml")
	if strings.Contains(content, "steps.trivy.outcome") && !strings.Contains(content, "id: trivy") {
		t.Fatal("docker-publish references steps.trivy.outcome but trivy step has no id: trivy")
	}
}

func TestGoReleaser_MainPathExists(t *testing.T) {
	content := mustReadText(t, ".goreleaser.yml")
	re := regexp.MustCompile(`(?m)^\s*main:\s*([^\s#]+)`)
	match := re.FindStringSubmatch(content)
	if len(match) != 2 {
		t.Fatal(".goreleaser.yml missing builds.main")
	}
	mainPath := strings.TrimSpace(match[1])
	mainPath = strings.Trim(mainPath, `"'`)
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf(".goreleaser.yml main path %q does not exist: %v", mainPath, err)
	}
}
