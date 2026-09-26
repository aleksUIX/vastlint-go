package prebid

import (
	"context"
	"fmt"
	"strings"

	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
)

type finding struct {
	ID            string
	RevenueImpact bool
}

// HandleRawBidderResponseHook checks video adm that looks like VAST.
// Every finding is counted. Bids are dropped only when reject_revenue is
// set and a revenue-impact rule fired.
func (m Module) HandleRawBidderResponseHook(
	ctx context.Context,
	miCtx hookstage.ModuleInvocationContext,
	payload hookstage.RawBidderResponsePayload,
) (hookstage.HookResult[hookstage.RawBidderResponsePayload], error) {
	result := hookstage.HookResult[hookstage.RawBidderResponsePayload]{}
	reject, err := m.reject(miCtx.AccountConfig)
	if err != nil {
		return result, err
	}
	if m.validate == nil || payload.BidderResponse == nil {
		return result, nil
	}

	rec := m.record
	if rec == nil {
		rec = currentRecorder()
	}
	caller := payload.Bidder
	if caller == "" {
		caller = "unknown"
	}

	kept := make([]*adapters.TypedBid, 0, len(payload.BidderResponse.Bids))
	dropped := false
	for _, bid := range payload.BidderResponse.Bids {
		if bid == nil || bid.Bid == nil || bid.BidType != openrtb_ext.BidTypeVideo {
			rec.Bid(caller, bidSkipped)
			kept = append(kept, bid)
			continue
		}

		xml, skip, ferr := m.markup(ctx, bid.Bid.AdM)
		if skip {
			rec.Bid(caller, bidSkipped)
			kept = append(kept, bid)
			continue
		}
		if ferr != nil {
			result.Errors = append(result.Errors, ferr.Error())
			rec.Bid(caller, bidError)
			kept = append(kept, bid)
			continue
		}

		issues, verr := m.validate(xml)
		if verr != nil {
			result.Errors = append(result.Errors, verr.Error())
			rec.Bid(caller, bidError)
			kept = append(kept, bid)
			continue
		}

		var revenueIDs []string
		for _, issue := range issues {
			if issue.RevenueImpact {
				revenueIDs = append(revenueIDs, issue.ID)
			}
			rec.Finding(caller, issue.ID, issue.RevenueImpact)
		}
		if reject && len(revenueIDs) > 0 {
			dropped = true
			rec.Bid(caller, bidRejected)
			result.DebugMessages = append(result.DebugMessages, fmt.Sprintf(
				"openadtech.vastlint dropped bid %s from %s: %s",
				bid.Bid.ID,
				caller,
				strings.Join(revenueIDs, ", "),
			))
			continue
		}
		rec.Bid(caller, bidChecked)
		kept = append(kept, bid)
	}

	if dropped {
		result.ChangeSet.RawBidderResponse().Bids().UpdateBids(kept)
	}
	return result, nil
}

func (m Module) markup(ctx context.Context, adm string) (string, bool, error) {
	trimmed := strings.TrimSpace(adm)
	if looksLikeVAST(trimmed) {
		return trimmed, false, nil
	}
	if !looksLikeTagURL(trimmed) {
		return "", true, nil
	}
	fetch := m.fetch
	if fetch == nil {
		fetch = fetchTag
	}
	body, err := fetch(ctx, trimmed, m.cfg.fetchTimeout())
	if err != nil {
		return "", false, err
	}
	return body, false, nil
}

func (m Module) reject(account []byte) (bool, error) {
	if len(account) == 0 {
		return m.cfg.RejectRevenue, nil
	}
	cfg, err := parseConfig(account)
	if err != nil {
		return false, err
	}
	return cfg.RejectRevenue, nil
}
