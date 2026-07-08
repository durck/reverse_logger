package store

import (
	"context"
	"testing"
	"time"

	"github.com/durck/reverse_logger/internal/edgehealth"
)

func TestEdgeHealthTransitionsOKDegradedDownOK(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	okReport := edgeHealthReport(edgehealth.StatusOK, base, edgehealth.Check{
		Name:     "reverse_ssh_tcp",
		Status:   edgehealth.CheckStatusOK,
		Required: true,
	})
	_, shouldAlert, err := st.RecordEdgeHealthReport(okReport, []byte(`{}`), base)
	if err != nil {
		t.Fatal(err)
	}
	if shouldAlert {
		t.Fatal("first ok report should not alert")
	}

	degradedReport := edgeHealthReport(edgehealth.StatusDegraded, base.Add(30*time.Second), edgehealth.Check{
		Name:     "logger_health",
		Status:   edgehealth.CheckStatusFailed,
		Required: true,
		Message:  "connection refused",
	})
	transition, shouldAlert, err := st.RecordEdgeHealthReport(degradedReport, []byte(`{}`), base.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !shouldAlert || transition.PreviousStatus != edgehealth.StatusOK || transition.CurrentStatus != edgehealth.StatusDegraded {
		t.Fatalf("unexpected degraded transition alert=%v transition=%+v", shouldAlert, transition)
	}

	_, shouldAlert, err = st.RecordEdgeHealthReport(degradedReport, []byte(`{}`), base.Add(31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if shouldAlert {
		t.Fatal("repeated degraded report should not alert")
	}

	transitions, err := st.EvaluateEdgeHealthTransitions(context.Background(), base.Add(122*time.Second), 30*time.Second, 3, DefaultEdgeHealthBootstrapGrace)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 || transitions[0].PreviousStatus != edgehealth.StatusDegraded || transitions[0].CurrentStatus != edgehealth.StatusDown {
		t.Fatalf("unexpected stale transitions: %+v", transitions)
	}

	recovery := edgeHealthReport(edgehealth.StatusOK, base.Add(123*time.Second), edgehealth.Check{
		Name:     "reverse_ssh_tcp",
		Status:   edgehealth.CheckStatusOK,
		Required: true,
	})
	transition, shouldAlert, err = st.RecordEdgeHealthReport(recovery, []byte(`{}`), base.Add(123*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !shouldAlert || transition.PreviousStatus != edgehealth.StatusDown || transition.CurrentStatus != edgehealth.StatusOK {
		t.Fatalf("unexpected recovery transition alert=%v transition=%+v", shouldAlert, transition)
	}
}

func TestEdgeHealthOverviewCountsExpectedUnknownAndDown(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	if err := st.RegisterEdgeHealthExpected([]edgehealth.ExpectedNode{{VPSName: "vps-1"}}, 2*time.Minute, base); err != nil {
		t.Fatal(err)
	}
	overview, err := st.EdgeHealthOverview(context.Background(), base.Add(time.Minute), 30*time.Second, 3, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Summary.Unknown != 1 || overview.Summary.Down != 0 {
		t.Fatalf("inside bootstrap summary = %+v", overview.Summary)
	}
	overview, err = st.EdgeHealthOverview(context.Background(), base.Add(3*time.Minute), 30*time.Second, 3, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Summary.Down != 1 || overview.Summary.Unknown != 0 {
		t.Fatalf("after bootstrap summary = %+v", overview.Summary)
	}
}

func edgeHealthReport(status string, checkedAt time.Time, checks ...edgehealth.Check) edgehealth.Report {
	return edgehealth.Report{
		VPSName:         "vps-1",
		VPSPublicIP:     "203.0.113.20",
		VPSInternalIP:   "192.0.2.2",
		Status:          status,
		Checks:          checks,
		IntervalSeconds: 30,
		MissedReports:   3,
		CheckedAt:       checkedAt,
	}
}
