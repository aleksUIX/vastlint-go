package prebid

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxTagBytes = 512 << 10

func looksLikeTagURL(adm string) bool {
	return strings.HasPrefix(adm, "https://") || strings.HasPrefix(adm, "http://")
}

func fetchTag(ctx context.Context, rawURL string, limit time.Duration) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("vastlint: tag url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("vastlint: tag url scheme %s is not http", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("vastlint: tag url has no host")
	}
	if blockedTagHost(parsed.Hostname()) {
		return "", fmt.Errorf("vastlint: refused tag url host %s", parsed.Hostname())
	}

	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("vastlint: tag url: %w", err)
	}
	resp, err := tagClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vastlint: tag fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("vastlint: tag fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTagBytes+1))
	if err != nil {
		return "", fmt.Errorf("vastlint: tag fetch: %w", err)
	}
	if len(body) > maxTagBytes {
		return "", fmt.Errorf("vastlint: tag url body exceeds %d bytes", maxTagBytes)
	}
	return string(body), nil
}

var tagClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("vastlint: tag url stopped after 3 redirects")
		}
		if blockedTagHost(req.URL.Hostname()) {
			return fmt.Errorf("vastlint: refused tag url host %s", req.URL.Hostname())
		}
		return nil
	},
}

func blockedTagHost(host string) bool {
	if strings.EqualFold(host, "metadata.google.internal") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return false
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
