package server

import (
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestControlDeliveryCountersGetListWatchParity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := prometheus.NewRegistry()
		collector := bfdmetrics.NewCollector(reg)
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger, bfd.WithManagerMetrics(collector))
		defer mgr.Close()
		go mgr.RunDispatch(t.Context())
		cfg := watchStatusConfig()
		sess, err := mgr.CreateSession(t.Context(), cfg, bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		bad := &bfd.ControlPacket{
			State: bfd.StateDown, MyDiscriminator: 99, DesiredMinTxInterval: 1000000, AuthPresent: true,
		}
		if sess.RecvPacket(bad) != bfd.DeliveryQueued {
			t.Fatal("bad auth packet was not admitted")
		}
		synctest.Wait()
		sub, err := mgr.SubscribeStateChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if adminErr := sess.SetAdminDown(t.Context()); adminErr != nil {
			t.Fatal(adminErr)
		}
		synctest.Wait()
		server := &BFDServer{manager: mgr, logger: logger}
		get, err := server.getSessionByDiscriminator(sess.LocalDiscriminator())
		if err != nil {
			t.Fatal(err)
		}
		list, err := server.ListSessions(t.Context(), &bfdv1.ListSessionsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		watch := stateChangeToProto(<-sub).GetSession()
		for _, session := range []*bfdv1.BfdSession{get.GetSession(), list.GetSessions()[0], watch} {
			if c := session.GetCounters(); c.GetPacketsDropped() != 1 || c.GetAuthFailures() != 1 ||
				c.GetPacketsReceived() != 0 {
				t.Errorf("Get/List/Watch counters = %v", c)
			}
		}
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == "gobfd_bfd_packets_dropped_total" || family.GetName() == "gobfd_bfd_auth_failures_total" {
				if len(family.GetMetric()) != 1 || family.GetMetric()[0].GetCounter().GetValue() != 1 {
					t.Errorf("session Prometheus projection=%v", family)
				}
			}
		}
		mgr.Close()
		list, err = server.ListSessions(t.Context(), &bfdv1.ListSessionsRequest{})
		if err != nil || len(list.GetSessions()) != 0 {
			t.Fatalf("removed session List=%v err=%v", list, err)
		}
		found := false
		for _, row := range list.GetDeliveryCounters() {
			if row.GetSource() == bfdv1.DeliverySource_DELIVERY_SOURCE_PACKET_OWNER &&
				row.GetOutcome() == bfdv1.DeliveryOutcome_DELIVERY_OUTCOME_AUTH_UNEXPECTED {
				found = row.GetCount() == 1
			}
		}
		if !found {
			t.Fatal("process ledger lost auth refusal after last session deletion")
		}
	})
}

func TestControlDeliveryProtoEnumIdentityAndZeroRows(t *testing.T) {
	t.Parallel()
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	s := &BFDServer{manager: mgr, logger: slog.New(slog.DiscardHandler)}
	list, err := s.ListSessions(t.Context(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetDeliveryCounters()) != len(bfd.DeliveryCounterPairs()) {
		t.Fatal("List omitted zero delivery rows")
	}
	for i, pair := range bfd.DeliveryCounterPairs() {
		row := list.GetDeliveryCounters()[i]
		if row.GetCount() != 0 || row.GetSource().String() != "DELIVERY_SOURCE_"+strings.ToUpper(pair.Source.String()) ||
			row.GetOutcome().String() != "DELIVERY_OUTCOME_"+strings.ToUpper(pair.Outcome.String()) {
			t.Errorf("proto enum/stable row order mismatch: %v versus %+v", row, pair)
		}
	}
}
