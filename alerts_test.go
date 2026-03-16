package main

import (
	"os"
	"strings"
	"testing"
)

func TestAlertRulesFile_Exists(t *testing.T) {
	info, err := os.Stat("alerts.rules.yml")
	if err != nil {
		t.Fatalf("alerts.rules.yml does not exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("alerts.rules.yml is empty")
	}
}

func TestAlertRulesFile_ContainsRequiredAlerts(t *testing.T) {
	data, err := os.ReadFile("alerts.rules.yml")
	if err != nil {
		t.Fatalf("failed to read alerts.rules.yml: %v", err)
	}
	content := string(data)

	required := []string{
		"MetricsAggregatorAllEndpointsDown",
		"MetricsAggregatorEndpointDown",
		"MetricsAggregatorHighErrorRate",
		"MetricsAggregatorSlowScrape",
		"MetricsAggregatorHighInvalidSamples",
	}
	for _, alert := range required {
		if !strings.Contains(content, alert) {
			t.Errorf("alerts.rules.yml missing required alert: %s", alert)
		}
	}
}

func TestAlertRulesFile_UsesCorrectMetricNames(t *testing.T) {
	data, err := os.ReadFile("alerts.rules.yml")
	if err != nil {
		t.Fatalf("failed to read alerts.rules.yml: %v", err)
	}
	content := string(data)

	metrics := []string{
		"scrape_success",
		"scrape_errors_total",
		"scrape_duration_seconds_bucket",
	}
	for _, m := range metrics {
		if !strings.Contains(content, m) {
			t.Errorf("alerts.rules.yml missing metric reference: %s", m)
		}
	}
}

func TestRunbook_Exists(t *testing.T) {
	info, err := os.Stat("runbook.md")
	if err != nil {
		t.Fatalf("runbook.md does not exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("runbook.md is empty")
	}
}

func TestRunbook_ContainsRequiredSections(t *testing.T) {
	data, err := os.ReadFile("runbook.md")
	if err != nil {
		t.Fatalf("failed to read runbook.md: %v", err)
	}
	content := string(data)

	sections := []string{
		"## MetricsAggregatorAllEndpointsDown",
		"## MetricsAggregatorEndpointDown",
		"## MetricsAggregatorHighErrorRate",
		"## MetricsAggregatorSlowScrape",
		"## MetricsAggregatorHighInvalidSamples",
		"## SLO Targets",
	}
	for _, section := range sections {
		if !strings.Contains(content, section) {
			t.Errorf("runbook.md missing required section: %s", section)
		}
	}
}
