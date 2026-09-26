package prebid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/require"
)

func TestFetchTagURLRejectsRevenueFinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(missingImpression))
	}))
	defer srv.Close()

	tally := &memTally{}
	m := Module{
		cfg: config{RejectRevenue: true, FetchTimeoutMs: 200},
		validate: func(xml string) ([]finding, error) {
			require.Contains(t, xml, "<VAST")
			return []finding{{ID: "VAST-2.0-inline-impression", RevenueImpact: true}}, nil
		},
		record: tally,
		fetch:  fetchTag,
	}
	payload := hookstage.RawBidderResponsePayload{
		Bidder: "dsp",
		BidderResponse: &adapters.BidderResponse{Bids: []*adapters.TypedBid{{
			BidType: openrtb_ext.BidTypeVideo,
			Bid:     &openrtb2.Bid{ID: "url", ImpID: "imp-1", AdM: srv.URL},
		}}},
	}

	result, err := m.HandleRawBidderResponseHook(context.Background(), hookstage.ModuleInvocationContext{}, payload)
	require.NoError(t, err)
	require.Empty(t, applyBids(t, payload, result).BidderResponse.Bids)
	require.Equal(t, []string{"dsp|" + bidRejected}, tally.bids)
	require.Equal(t, []string{"dsp|VAST-2.0-inline-impression|true"}, tally.findings)
}

func TestFetchFailureCountsAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	tally := &memTally{}
	m := Module{
		cfg:      config{RejectRevenue: true, FetchTimeoutMs: 200},
		validate: func(string) ([]finding, error) { t.Fatal("validate"); return nil, nil },
		record:   tally,
		fetch:    fetchTag,
	}
	payload := hookstage.RawBidderResponsePayload{
		Bidder: "dsp",
		BidderResponse: &adapters.BidderResponse{Bids: []*adapters.TypedBid{{
			BidType: openrtb_ext.BidTypeVideo,
			Bid:     &openrtb2.Bid{ID: "url", AdM: srv.URL},
		}}},
	}

	result, err := m.HandleRawBidderResponseHook(context.Background(), hookstage.ModuleInvocationContext{}, payload)
	require.NoError(t, err)
	require.Empty(t, result.ChangeSet.Mutations())
	require.NotEmpty(t, result.Errors)
	require.Equal(t, []string{"dsp|" + bidError}, tally.bids)
	require.Empty(t, tally.findings)
}

func TestRefusesLinkLocalTagHost(t *testing.T) {
	_, err := fetchTag(context.Background(), "http://169.254.169.254/latest/meta-data", time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refused")
}

func TestFetchHonorsDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			_, _ = w.Write([]byte(missingImpression))
		}
	}))
	defer srv.Close()

	start := time.Now()
	_, err := fetchTag(context.Background(), srv.URL, 30*time.Millisecond)
	require.Error(t, err)
	require.Less(t, time.Since(start), 500*time.Millisecond)
}
