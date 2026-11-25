package handlers

import (
	"testing"

	"github.com/Pamawas/pamawas-ingest/models"
)

func TestParseGrafanaPayloadStatus(t *testing.T) {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus string
	}{
		{
			name: "top-level status",
			payload: map[string]interface{}{
				"status": "resolved",
				"state":  "ok",
			},
			wantStatus: "resolved",
		},
		{
			name: "legacy state",
			payload: map[string]interface{}{
				"state": "ok",
			},
			wantStatus: "resolved",
		},
		{
			name: "alert status",
			payload: map[string]interface{}{
				"alerts": []interface{}{
					map[string]interface{}{
						"status": "resolved",
					},
				},
			},
			wantStatus: "resolved",
		},
		{
			name: "alert status with custom fields",
			payload: map[string]interface{}{
				"ruleName":  "High CPU Usage",
				"timestamp": "2026-08-16T01:52:00Z",
				"tags":      map[string]interface{}{},
				"alerts": []interface{}{
					map[string]interface{}{
						"status": "resolved",
					},
				},
			},
			wantStatus: "resolved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseGrafanaPayload(tt.payload)
			got := parsed.ToWebhookRequest().Status
			if got != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

func TestParseGrafanaPayloadResolutionTimeAndFingerprint(t *testing.T) {
	payload := map[string]interface{}{
		"status": "resolved",
		"alerts": []interface{}{
			map[string]interface{}{
				"status":      "resolved",
				"startsAt":    "2026-08-16T01:47:00Z",
				"endsAt":      "2026-08-16T01:52:00Z",
				"fingerprint": "stable-alert-fingerprint",
			},
		},
	}

	parsed := parseGrafanaPayload(payload)
	req := parsed.ToWebhookRequest()
	if req.OccurredAt != "2026-08-16T01:52:00Z" {
		t.Fatalf("occurred at = %q, want resolved time", req.OccurredAt)
	}
	if req.Labels["grafana_fingerprint"] != "stable-alert-fingerprint" {
		t.Fatalf("Grafana fingerprint label = %q, want stable alert fingerprint", req.Labels["grafana_fingerprint"])
	}
}

func TestParseGrafanaPayloadConditionReference(t *testing.T) {
	payload := map[string]interface{}{
		"orgId": float64(1),
		"alerts": []interface{}{
			map[string]interface{}{
				"orgId":       float64(1),
				"ruleUID":     "rule-uid",
				"fingerprint": "alert-fingerprint",
				"labels": map[string]interface{}{
					"service": "api",
				},
			},
		},
	}

	parsed := parseGrafanaPayload(payload)
	ref := parsed.ToWebhookRequest().ConditionRef
	if ref == nil || ref.ResourceID != "rule-uid" || ref.InstanceID != "alert-fingerprint" {
		t.Fatalf("condition reference = %#v", ref)
	}
	if ref.Labels["service"] != "api" || ref.TenantID != "1" {
		t.Fatalf("condition reference fields = %#v", ref)
	}
}

func TestParseGrafanaPayloadConditionReferenceAcceptsSourceIdentityAliases(t *testing.T) {
	parsed := parseGrafanaPayload(map[string]interface{}{
		"orgId": "7",
		"alerts": []interface{}{
			map[string]interface{}{
				"fingerprint": "alert-fingerprint",
				"labels": map[string]interface{}{
					"__alert_rule_uid__": "rule-from-source-label",
				},
			},
		},
	})
	ref := parsed.ToWebhookRequest().ConditionRef
	if ref == nil || ref.ResourceID != "rule-from-source-label" || ref.TenantID != "7" {
		t.Fatalf("condition reference aliases = %#v", ref)
	}
}

func TestParseGrafanaPayloadMatchesGroupedConditionToNormalizedEvent(t *testing.T) {
	parsed := parseGrafanaPayload(map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"orgId": float64(1), "ruleUID": "rule-a", "fingerprint": "fp-a",
			},
			map[string]interface{}{
				"orgId": float64(1), "ruleUID": "rule-b", "fingerprint": "fp-b",
			},
		},
	})
	req := parsed.ToWebhookRequest()
	if req.Labels["grafana_fingerprint"] != "fp-a" {
		t.Fatalf("normalized fingerprint = %q, want fp-a", req.Labels["grafana_fingerprint"])
	}
	if req.ConditionRef == nil || req.ConditionRef.ResourceID != "rule-a" || req.ConditionRef.InstanceID != "fp-a" {
		t.Fatalf("condition reference must match the normalized alert: %#v", req.ConditionRef)
	}
}

// TestUnknownThresholdRanksBelowEverySeverity pins the gate's floor.
//
// HIGH_SEVERITY_THRESHOLD=unknown means "no gate": it must rank below debug so
// that an event with no severity at all (rank 0) still passes the comparison
// `severityRank(event) >= severityRank(threshold)`.
func TestUnknownThresholdRanksBelowEverySeverity(t *testing.T) {
	if got := severityRank("unknown"); got != 0 {
		t.Fatalf("severityRank(unknown) = %d, want 0", got)
	}
	for _, severity := range []string{"", "debug", "info", "warning", "high", "critical"} {
		if severityRank(severity) < severityRank("unknown") {
			t.Errorf("severity %q would be filtered by the unknown threshold", severity)
		}
	}
}

// TestTargetAndServiceResolveFromWhateverLabelsTheRuleHas covers the real
// label shapes in production.
//
// Alert rules are written by hand against several data sources, so there is no
// single label that names the affected entity. Reading only service/instance/job
// left pod-scoped alerts with no entity at all, and the report card read
// "Pod Crash Looping on unknown".
func TestTargetAndServiceResolveFromWhateverLabelsTheRuleHas(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]interface{}
		wantTarget  string
		wantService string
	}{
		{
			name:        "scrape target",
			tags:        map[string]interface{}{"instance": "koba-labs", "job": "docker_cadvisor", "cluster": "docker_cluster"},
			wantTarget:  "koba-labs",
			wantService: "docker_cadvisor",
		},
		{
			name:        "ceph daemon",
			tags:        map[string]interface{}{"instance": "ceph-1", "job": "ceph", "ceph_daemon": "osd.3"},
			wantTarget:  "ceph-1",
			wantService: "ceph",
		},
		{
			// The shape that produced "on unknown": no instance, no job.
			name:        "pod scoped",
			tags:        map[string]interface{}{"pod": "pamawas-ingest-869ffc8f75-bkhvq", "namespace": "pamawas"},
			wantTarget:  "pamawas-ingest-869ffc8f75-bkhvq",
			wantService: "pamawas",
		},
		{
			// A kube alert that also carries an app label prefers the app as
			// the owner: it is the name that appears in metrics and logs.
			name: "pod with app",
			tags: map[string]interface{}{
				"app": "pamawas-ingest", "pod": "pamawas-ingest-869ffc8f75-bkhvq",
				"namespace": "pamawas", "instance": "10.41.34.63:8080", "job": "pamawas",
			},
			wantTarget:  "10.41.34.63:8080",
			wantService: "pamawas-ingest",
		},
		{
			name:        "explicit service wins",
			tags:        map[string]interface{}{"service": "payment-api", "instance": "10.0.0.4"},
			wantTarget:  "10.0.0.4",
			wantService: "payment-api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := (&models.GrafanaWebhookRequest{
				RuleName: "Rule",
				Tags:     toTags(tt.tags),
			}).ToWebhookRequest()
			if req.Target != tt.wantTarget {
				t.Errorf("target = %q, want %q", req.Target, tt.wantTarget)
			}
			if req.Service != tt.wantService {
				t.Errorf("service = %q, want %q", req.Service, tt.wantService)
			}
		})
	}
}

func toTags(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
