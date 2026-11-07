package doh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const recordTypeA = 1

// Client queries Tencent DoH (doh.pub) with an EDNS client subnet.
type Client struct {
	url  string
	http *http.Client
}

// New creates a DoH client. Empty url falls back to https://doh.pub/dns-query.
func New(rawURL string, timeout time.Duration) *Client {
	if rawURL == "" {
		rawURL = "https://doh.pub/dns-query"
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		url:  rawURL,
		http: &http.Client{Transport: transport, Timeout: timeout},
	}
}

// QueryA asks for A records for host using the given EDNS client subnet.
// It returns the records and the maximum TTL (0 when absent).
//
// The query string is assembled manually because edns_client_subnet's "/" must
// remain unescaped; url.Values would encode it as %2F and doh.pub returns 400.
func (c *Client) QueryA(ctx context.Context, host, subnet string) ([]string, int, error) {
	query := "name=" + url.QueryEscape(host) + "&type=A"
	if subnet != "" {
		query += "&edns_client_subnet=" + quoteSubnet(subnet)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"?"+query, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/dns-json")

	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, 0, fmt.Errorf("DoH returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			TTL  int    `json:"TTL"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		return nil, 0, err
	}
	if payload.Status != 0 {
		return nil, 0, fmt.Errorf("DoH DNS status %d", payload.Status)
	}

	ips := make([]string, 0, len(payload.Answer))
	maxTTL := 0
	for _, item := range payload.Answer {
		if item.Type != recordTypeA || item.Data == "" {
			continue
		}
		ips = append(ips, item.Data)
		if item.TTL > maxTTL {
			maxTTL = item.TTL
		}
	}
	return ips, maxTTL, nil
}

func quoteSubnet(subnet string) string {
	escaped := url.QueryEscape(subnet)
	escaped = strings.ReplaceAll(escaped, "%2F", "/")
	return strings.ReplaceAll(escaped, "%2f", "/")
}
