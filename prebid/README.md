# vastlint-go/prebid

Prebid Server hook for video `adm`. The check runs in-process through [vastlint-go](https://github.com/aleksUIX/vastlint-go). It does not fetch wrapper chains and it does not call a hosted validator.

The module stays off until the host enables it. Counting does not remove bids. `reject_revenue` drops a video bid when a revenue-impact rule fires.

Prebid Server does not load hooks from an external repo on its own. The host imports this package into their server build. Build that server with cgo so the vastlint static library links.

```sh
go get github.com/aleksUIX/vastlint-go/prebid
```

## Wire it

In `modules/builder.go`:

```go
vastlintpbs "github.com/aleksUIX/vastlint-go/prebid"
```

```go
"openadtech": {
    "vastlint": vastlintpbs.Builder,
},
```

In `metrics/prometheus/prometheus.go`, after module metrics are created:

```go
if _, ok := moduleStageNames[vastlintpbs.MetricsKey]; ok {
    vastlintpbs.Register(reg, cfg.Namespace, cfg.Subsystem)
}
```

`Register` adds two series to the Prometheus registry Prebid Server scrapes on `/metrics`. `caller` is the bidder name.

`vastlint_findings_total{caller,rule_id,revenue_impact}` counts every finding. `revenue_impact` is `true` for the twelve rules that mark lost impressions, broken measurement, or zero fill. Rejects use that label. The other findings stay on the scrape.

`vastlint_bids_total{caller,result}` counts each bid as `checked`, `skipped`, `rejected`, or `error`. A clean tag and a skipped tag no longer look the same.

`revenue_impact` is the flag on the vastlint result. The hook does not keep its own copy of the rule list.

A video `adm` that is an `http` or `https` URL is fetched, then validated. The fetch stops at the earlier of `fetch_timeout_ms` (default 50) and the hook group timeout. A failed fetch counts as `error` and the bid stays. The group timeout has to be longer than the fetch or the stage cancels the hook before the counter is written. Link-local hosts are refused.

## Configuration

```yaml
hooks:
  enabled: true
  modules:
    openadtech:
      vastlint:
        enabled: true
        reject_revenue: false
        fetch_timeout_ms: 50
  host_execution_plan:
    endpoints:
      /openrtb2/auction:
        stages:
          raw_bidder_response:
            groups:
              - timeout: 80
                hook_sequence:
                  - module_code: openadtech.vastlint
                    hook_impl_code: vastlint-raw-bidder-response
      /openrtb2/video:
        stages:
          raw_bidder_response:
            groups:
              - timeout: 80
                hook_sequence:
                  - module_code: openadtech.vastlint
                    hook_impl_code: vastlint-raw-bidder-response
```

| Field | Default | Effect |
|---|---|---|
| `enabled` | false | Host opt-in. False skips the module. |
| `reject_revenue` | false | When true, a video bid with a revenue-impact finding is removed. Other bids in the response stay. |

An account module config of `{"reject_revenue": true}` replaces the host flag for that account.

Banner bids, empty `adm`, and tag URLs are skipped. A validator error leaves the bid in place and records the error on the hook outcome.
