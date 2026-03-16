package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGrafanaDashboard_Exists(t *testing.T) {
	info, err := os.Stat("grafana-dashboard.json")
	if err != nil {
		t.Fatalf("grafana-dashboard.json does not exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("grafana-dashboard.json is empty")
	}
}

func TestGrafanaDashboard_ValidJSON(t *testing.T) {
	data, err := os.ReadFile("grafana-dashboard.json")
	if err != nil {
		t.Fatalf("failed to read grafana-dashboard.json: %v", err)
	}
	var dashboard map[string]interface{}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("grafana-dashboard.json is not valid JSON: %v", err)
	}
}

func TestGrafanaDashboard_ContainsRequiredPanels(t *testing.T) {
	data, err := os.ReadFile("grafana-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	required := []string{
		"Scrape Success",
		"Scrape Duration",
		"Request Rate",
		"Error Rate",
		"Invalid Samples",
	}
	for _, panel := range required {
		if !strings.Contains(content, panel) {
			t.Errorf("grafana-dashboard.json missing required panel: %q", panel)
		}
	}
}

func TestGrafanaDashboard_ReferencesCorrectMetrics(t *testing.T) {
	data, err := os.ReadFile("grafana-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	metrics := []string{
		"scrape_success",
		"scrape_duration_seconds_bucket",
		"requests_total",
		"scrape_errors_total",
	}
	for _, m := range metrics {
		if !strings.Contains(content, m) {
			t.Errorf("grafana-dashboard.json missing metric reference: %q", m)
		}
	}
}
