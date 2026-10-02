package quota

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHistoryFlushQueryAndTop(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 90)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	store.lastFlush = base
	uplink, downlink := store.Counters(HistoryDimensions{InboundTag: "ss-in", UserName: "alice", InboundType: "shadowsocks", Protocol: "tls", Target: "example.com"})
	uplink.Add(600)
	downlink.Add(1200)
	if err = store.Flush(base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	uplink.Add(300)
	downlink.Add(300)
	if err = store.Flush(base.Add(time.Hour + time.Minute)); err != nil {
		t.Fatal(err)
	}

	points, err := store.Query(context.Background(), HistoryQuery{From: base, To: base.Add(24 * time.Hour), Granularity: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].UplinkBytes != 900 || points[0].DownlinkBytes != 1500 || points[0].ActiveUsers != 1 {
		t.Fatalf("unexpected points: %+v", points)
	}
	top, err := store.Top(context.Background(), HistoryQuery{From: base, To: base.Add(24 * time.Hour)}, "target", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Target != "example.com" || top[0].UsedBytes != 2400 {
		t.Fatalf("unexpected top: %+v", top)
	}
}

func TestRenderWeeklyHistory(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := NewManager()
	manager.SetHistory(store)
	now := time.Now().UTC()
	store.lastFlush = now.Add(-time.Minute)
	uplink, downlink := store.Counters(HistoryDimensions{InboundTag: "ss-in", UserName: "alice"})
	uplink.Add(1024)
	downlink.Add(2048)
	if err = store.Flush(now); err != nil {
		t.Fatal(err)
	}
	h := &portalOutbound{ctx: context.Background(), manager: manager}
	markup := h.renderWeeklyHistory(UserSnapshot{InboundTag: "ss-in", UserName: "alice"})
	if !strings.Contains(markup, "Last 7 days") || !strings.Contains(markup, "3.00 KB") || strings.Count(markup, "history-column") != 7 {
		t.Fatalf("unexpected chart markup: %s", markup)
	}
}

func TestHistoryBoundsTargetCardinality(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < maxHistorySeries+100; i++ {
		store.Counters(HistoryDimensions{InboundTag: "ss-in", UserName: "alice", Protocol: "tcp", Target: strconv.Itoa(i)})
	}
	if got := len(store.counters); got > maxHistorySeries+1 {
		t.Fatalf("history series grew to %d", got)
	}
	if store.counters[HistoryDimensions{InboundTag: "ss-in", UserName: "alice", Protocol: "tcp", Target: historyOtherTarget}] == nil {
		t.Fatal("missing overflow target")
	}
}

func TestHistoryRejectsInvalidGranularity(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Query(context.Background(), HistoryQuery{From: time.Now().Add(-time.Hour), To: time.Now(), Granularity: "week"})
	if err == nil {
		t.Fatal("expected invalid granularity error")
	}
}
