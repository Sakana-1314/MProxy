package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	stdlog "log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cdnproxy/internal/doh"
	"cdnproxy/internal/store"
)

const (
	dohQueryConcurrency       = 6
	dohProbeConcurrency       = 20
	dohProbeGlobalConcurrency = 32
	dohQueryRetryDelay        = 300 * time.Millisecond
	dohProbeTimeout           = 3 * time.Second
	dohResolveTimeout         = 15 * time.Second
	dohFailureTTL             = 15 * time.Second
	dohProbeTTL               = 300 * time.Second
	dohMinTTL                 = 30 * time.Second
	dohMaxTTL                 = 30 * time.Minute
)

type upstreamTarget struct {
	Scheme string
	Host   string
	Port   int
}

func (t upstreamTarget) key() string {
	return strings.ToLower(t.Scheme) + "|" + NormalizeHost(t.Host) + "|" + strconv.Itoa(t.Port)
}

func (t upstreamTarget) address() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

func targetsFromRules(rules []store.Rule) []upstreamTarget {
	targets := make([]upstreamTarget, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		for _, scheme := range upstreamSchemes(rule) {
			targets = append(targets, upstreamTarget{
				Scheme: scheme,
				Host:   NormalizeHost(rule.UpstreamHost),
				Port:   rule.UpstreamPort,
			})
		}
	}
	return targets
}

// upstreamSchemes expands "follow" so both possible upstream protocols are
// resolved and probed when the target accepts both.
func upstreamSchemes(rule store.Rule) []string {
	switch rule.UpstreamScheme {
	case store.SchemeHTTP, store.SchemeHTTPS:
		return []string{rule.UpstreamScheme}
	case store.UpstreamSchemeFollow:
		switch rule.TargetScheme {
		case store.SchemeHTTP:
			return []string{store.SchemeHTTP}
		case store.SchemeHTTPS:
			return []string{store.SchemeHTTPS}
		default:
			return []string{store.SchemeHTTP, store.SchemeHTTPS}
		}
	default:
		return []string{store.SchemeHTTPS}
	}
}

// ResolvedIP is one A record with the measured TCP/TLS latency.
type ResolvedIP struct {
	IP        string `json:"ip"`
	LatencyMS int64  `json:"latency_ms"`
	Reachable bool   `json:"reachable"`
}

// RegionStat is one regional DoH query result.
type RegionStat struct {
	Subnet    string `json:"subnet"`
	LatencyMS int64  `json:"latency_ms"`
	IPCount   int    `json:"ip_count"`
	Error     string `json:"error,omitempty"`
}

// ResolutionReport is the cached resolution returned by the rule test API.
type ResolutionReport struct {
	Host         string       `json:"host"`
	Scheme       string       `json:"scheme"`
	Port         int          `json:"port"`
	IPs          []ResolvedIP `json:"ips"`
	Regions      []RegionStat `json:"regions,omitempty"`
	DurationMS   int64        `json:"duration_ms"`
	ResolvedAt   int64        `json:"resolved_at"`
	TTLSeconds   int          `json:"ttl_seconds"`
	RegionOK     int          `json:"region_ok"`
	RegionFailed int          `json:"region_failed"`
	Error        string       `json:"error,omitempty"`
}

type cachedResolution struct {
	report     ResolutionReport
	expiresAt  time.Time
	refreshing bool
}

type resolveCall struct {
	done   chan struct{}
	report ResolutionReport
	err    error
}

type upstreamResolver struct {
	client  *doh.Client
	subnets []string
	logger  *stdlog.Logger
	store   *store.Store

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	cache    map[string]*cachedResolution
	inflight map[string]*resolveCall
	rr       map[string]uint64

	dohSem   chan struct{}
	probeSem chan struct{}

	// probeFn and dialFn are injectable for tests; production uses TCP/TLS.
	probeFn func(ctx context.Context, target upstreamTarget, ip string) (int64, bool)
	dialFn  func(ctx context.Context, network, address string) (net.Conn, error)
}

type resolverOptions struct {
	DOHURL  string
	Subnets []string
	Logger  *stdlog.Logger
	Store   *store.Store
}

func newUpstreamResolver(opts resolverOptions) *upstreamResolver {
	logger := opts.Logger
	if logger == nil {
		logger = stdlog.Default()
	}
	subnets := opts.Subnets
	if len(subnets) == 0 {
		subnets = doh.DefaultRegionSubnets
	}
	clean := make([]string, 0, len(subnets))
	for _, subnet := range subnets {
		subnet = strings.TrimSpace(subnet)
		if subnet != "" {
			clean = append(clean, subnet)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &upstreamResolver{
		client:   doh.New(opts.DOHURL, 6*time.Second),
		subnets:  clean,
		logger:   logger,
		store:    opts.Store,
		ctx:      ctx,
		cancel:   cancel,
		cache:    make(map[string]*cachedResolution),
		inflight: make(map[string]*resolveCall),
		rr:       make(map[string]uint64),
		dohSem:   make(chan struct{}, dohQueryConcurrency),
		probeSem: make(chan struct{}, dohProbeGlobalConcurrency),
	}
}

func (r *upstreamResolver) Close() { r.cancel() }

// Warm resolves enabled upstreams in the background without touching the
// request path. Targets are handled sequentially; DoH queries and probes are
// bounded globally inside the resolver.
func (r *upstreamResolver) Warm(targets []upstreamTarget) {
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.Host == "" || target.Port <= 0 {
			continue
		}
		key := target.key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		r.loadPersisted(target)
		r.mu.Lock()
		entry := r.cache[key]
		fresh := entry != nil && time.Now().Before(entry.expiresAt)
		r.mu.Unlock()
		if fresh {
			continue
		}

		ctx, cancel := context.WithTimeout(r.ctx, dohResolveTimeout+5*time.Second)
		report, err := r.resolveShared(ctx, target)
		cancel()
		if err != nil && (report == nil || report.Error == "") {
			r.logger.Printf("[doh] %s 预解析失败: %v", target.Host, err)
		}
	}
}

// Report returns the cached resolution, or resolves if there is no usable
// entry. Stale entries are returned immediately while a refresh runs.
func (r *upstreamResolver) Report(ctx context.Context, target upstreamTarget) (*ResolutionReport, error) {
	r.loadPersisted(target)
	key := target.key()
	r.mu.Lock()
	entry := r.cache[key]
	r.mu.Unlock()
	if entry != nil {
		report := entry.report
		if time.Now().After(entry.expiresAt) && len(report.IPs) > 0 {
			r.triggerRefresh(target)
		}
		if report.Error != "" && len(report.IPs) == 0 {
			return &report, errors.New(report.Error)
		}
		return &report, nil
	}
	return r.resolveShared(ctx, target)
}

func (r *upstreamResolver) resolveForDial(ctx context.Context, target upstreamTarget) (*ResolutionReport, error) {
	r.loadPersisted(target)
	key := target.key()
	r.mu.Lock()
	entry := r.cache[key]
	r.mu.Unlock()
	if entry == nil {
		return r.resolveShared(ctx, target)
	}
	report := entry.report
	if time.Now().Before(entry.expiresAt) {
		return &report, nil
	}
	if len(report.IPs) > 0 {
		r.triggerRefresh(target)
		return &report, nil
	}
	return r.resolveShared(ctx, target)
}

// Dial returns a TCP connection to one of the resolved IPs, trying them in the
// measured latency order. The HTTP transport still performs TLS with the
// upstream host as ServerName, so SNI and Host stay on the CDN domain.
func (r *upstreamResolver) Dial(ctx context.Context, target upstreamTarget) (net.Conn, error) {
	report, err := r.resolveForDial(ctx, target)
	if report == nil || len(report.IPs) == 0 {
		if err == nil {
			err = errors.New("upstream resolution returned no IPs")
		}
		if report != nil && report.Error != "" {
			err = errors.New(report.Error)
		}
		return nil, fmt.Errorf("resolve %s: %w", target.Host, err)
	}

	reachable := make([]ResolvedIP, 0, len(report.IPs))
	fallback := make([]ResolvedIP, 0, len(report.IPs))
	for _, item := range report.IPs {
		if item.Reachable {
			reachable = append(reachable, item)
		} else {
			fallback = append(fallback, item)
		}
	}
	primary := reachable
	if len(primary) == 0 {
		primary = fallback
		fallback = nil
	}
	if len(primary) == 0 {
		return nil, fmt.Errorf("dial upstream %s: no usable IPs", target.Host)
	}

	// Every new upstream connection starts at the next reachable IP, so all
	// regional IPs are used evenly instead of always picking the lowest-latency
	// (usually local) one. Latency order is only used for failover within a dial.
	start := r.nextStart(target.key())
	ordered := rotatedCandidates(primary, start)
	ordered = append(ordered, fallback...)

	dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
	dial := func(network, address string) (net.Conn, error) {
		if r.dialFn != nil {
			return r.dialFn(ctx, network, address)
		}
		return dialer.DialContext(ctx, network, address)
	}
	var lastErr error
	for _, item := range ordered {
		conn, dialErr := dial("tcp", net.JoinHostPort(item.IP, strconv.Itoa(target.Port)))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("no reachable upstream IP")
	}
	return nil, fmt.Errorf("dial upstream %s: %w", target.Host, lastErr)
}

func (r *upstreamResolver) triggerRefresh(target upstreamTarget) {
	key := target.key()
	r.mu.Lock()
	entry := r.cache[key]
	if entry == nil || entry.refreshing {
		r.mu.Unlock()
		return
	}
	if _, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		return
	}
	entry.refreshing = true
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			if current := r.cache[key]; current != nil {
				current.refreshing = false
			}
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(r.ctx, dohResolveTimeout)
		defer cancel()
		if _, err := r.resolveShared(ctx, target); err != nil {
			r.logger.Printf("[doh] 刷新 %s 失败: %v", target.Host, err)
		}
	}()
}

func (r *upstreamResolver) resolveShared(ctx context.Context, target upstreamTarget) (*ResolutionReport, error) {
	key := target.key()
	r.mu.Lock()
	if call, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			report := call.report
			return &report, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &resolveCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.mu.Unlock()

	report, err := r.doResolve(ctx, target)
	if report != nil {
		call.report = *report
	}
	call.err = err

	if report != nil && report.Error == "" && len(report.IPs) > 0 {
		r.persist(target, report)
	}

	r.mu.Lock()
	delete(r.inflight, key)
	if report != nil {
		ttl := time.Duration(report.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = dohFailureTTL
		}
		r.cache[key] = &cachedResolution{report: *report, expiresAt: time.Now().Add(ttl)}
	}
	r.mu.Unlock()
	close(call.done)
	return report, err
}

func (r *upstreamResolver) doResolve(ctx context.Context, target upstreamTarget) (*ResolutionReport, error) {
	start := time.Now()
	report := &ResolutionReport{
		Host:       target.Host,
		Scheme:     target.Scheme,
		Port:       target.Port,
		ResolvedAt: start.Unix(),
	}

	if net.ParseIP(target.Host) != nil {
		latency, reachable := r.probeOne(ctx, target, target.Host)
		report.IPs = []ResolvedIP{{IP: target.Host, LatencyMS: latency, Reachable: reachable}}
		report.TTLSeconds = int(dohProbeTTL / time.Second)
		report.DurationMS = time.Since(start).Milliseconds()
		return report, nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, dohResolveTimeout)
	defer cancel()

	results := r.querySubnets(resolveCtx, target.Host)
	report.Regions = make([]RegionStat, len(results))
	collected := make([]string, 0, len(results)*2)
	maxTTL := 0
	for index, result := range results {
		stat := RegionStat{
			Subnet:    r.subnets[index],
			LatencyMS: result.latencyMS,
			IPCount:   len(result.ips),
		}
		if result.err != nil {
			stat.Error = result.err.Error()
			report.RegionFailed++
		} else {
			report.RegionOK++
			collected = append(collected, result.ips...)
			if result.ttl > maxTTL {
				maxTTL = result.ttl
			}
		}
		report.Regions[index] = stat
	}

	unique := dedupeStrings(collected)
	if len(unique) == 0 {
		report.Error = fmt.Sprintf("DoH returned no A records (%d/%d regions failed)", report.RegionFailed, len(results))
		report.TTLSeconds = int(dohFailureTTL / time.Second)
		report.DurationMS = time.Since(start).Milliseconds()
		r.logger.Printf("[doh] %s -> no IPs, regions %d/%d failed, duration=%dms",
			target.Host, report.RegionFailed, len(results), report.DurationMS)
		return report, errors.New(report.Error)
	}

	report.IPs = r.probeAll(resolveCtx, target, unique)
	ttl := time.Duration(maxTTL) * time.Second
	if ttl <= 0 {
		ttl = dohProbeTTL
	}
	if ttl < dohMinTTL {
		ttl = dohMinTTL
	}
	if ttl > dohMaxTTL {
		ttl = dohMaxTTL
	}
	report.TTLSeconds = int(ttl / time.Second)
	report.DurationMS = time.Since(start).Milliseconds()

	reachable := 0
	best := int64(-1)
	for _, item := range report.IPs {
		if item.Reachable {
			reachable++
			if best < 0 || item.LatencyMS < best {
				best = item.LatencyMS
			}
		}
	}
	r.logger.Printf("[doh] %s://%s:%d -> %d IPs (%d reachable), regions %d/%d, best=%dms, duration=%dms",
		target.Scheme, target.Host, target.Port, len(report.IPs), reachable,
		report.RegionOK, len(results), best, report.DurationMS)
	return report, nil
}

type subnetResult struct {
	ips       []string
	ttl       int
	latencyMS int64
	err       error
}

func (r *upstreamResolver) querySubnets(ctx context.Context, host string) []subnetResult {
	results := make([]subnetResult, len(r.subnets))
	if len(r.subnets) == 0 {
		return results
	}
	indexes := make(chan int)
	workers := dohQueryConcurrency
	if workers > len(r.subnets) {
		workers = len(r.subnets)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexes {
				results[index] = r.querySubnet(ctx, host, r.subnets[index])
			}
		}()
	}
	for index := range r.subnets {
		indexes <- index
	}
	close(indexes)
	wg.Wait()

	failed := make([]int, 0)
	for index, result := range results {
		if result.err != nil {
			failed = append(failed, index)
		}
	}
	if len(failed) == 0 || ctx.Err() != nil {
		return results
	}
	timer := time.NewTimer(dohQueryRetryDelay)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
		return results
	}
	for _, index := range failed {
		if ctx.Err() != nil {
			break
		}
		results[index] = r.querySubnet(ctx, host, r.subnets[index])
	}
	return results
}

func (r *upstreamResolver) querySubnet(ctx context.Context, host, subnet string) subnetResult {
	select {
	case r.dohSem <- struct{}{}:
	case <-ctx.Done():
		return subnetResult{err: ctx.Err()}
	}
	start := time.Now()
	ips, ttl, err := r.client.QueryA(ctx, host, subnet)
	latency := time.Since(start).Milliseconds()
	<-r.dohSem
	return subnetResult{ips: ips, ttl: ttl, latencyMS: latency, err: err}
}

func (r *upstreamResolver) probeAll(ctx context.Context, target upstreamTarget, ips []string) []ResolvedIP {
	out := make([]ResolvedIP, len(ips))
	indexes := make(chan int)
	workers := dohProbeConcurrency
	if workers > len(ips) {
		workers = len(ips)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexes {
				latency, reachable := r.probeOne(ctx, target, ips[index])
				out[index] = ResolvedIP{IP: ips[index], LatencyMS: latency, Reachable: reachable}
			}
		}()
	}
	for index := range ips {
		indexes <- index
	}
	close(indexes)
	wg.Wait()

	sortResolvedIPs(out)
	return out
}

func sortResolvedIPs(ips []ResolvedIP) {
	sort.SliceStable(ips, func(i, j int) bool {
		if ips[i].Reachable != ips[j].Reachable {
			return ips[i].Reachable
		}
		if ips[i].Reachable {
			return ips[i].LatencyMS < ips[j].LatencyMS
		}
		return ips[i].IP < ips[j].IP
	})
}

func rotatedCandidates(ips []ResolvedIP, start uint64) []ResolvedIP {
	if len(ips) == 0 {
		return nil
	}
	offset := int(start % uint64(len(ips)))
	out := make([]ResolvedIP, 0, len(ips))
	for index := 0; index < len(ips); index++ {
		out = append(out, ips[(offset+index)%len(ips)])
	}
	return out
}

func (r *upstreamResolver) nextStart(key string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.rr[key]
	r.rr[key] = value + 1
	return value
}

func (r *upstreamResolver) loadPersisted(target upstreamTarget) {
	if r.store == nil {
		return
	}
	key := target.key()
	r.mu.Lock()
	_, exists := r.cache[key]
	r.mu.Unlock()
	if exists {
		return
	}
	saved, err := r.store.LoadUpstreamResolution(target.Scheme, target.Host, target.Port)
	if err != nil {
		r.logger.Printf("[doh] 读取持久化解析结果失败 %s: %v", target.Host, err)
		return
	}
	if saved == nil || len(saved.IPs) == 0 {
		return
	}
	report := ResolutionReport{
		Host:         target.Host,
		Scheme:       target.Scheme,
		Port:         target.Port,
		DurationMS:   saved.DurationMS,
		ResolvedAt:   saved.ResolvedAt,
		TTLSeconds:   saved.TTLSeconds,
		RegionOK:     saved.RegionOK,
		RegionFailed: saved.RegionFailed,
		Error:        saved.Error,
	}
	report.IPs = make([]ResolvedIP, 0, len(saved.IPs))
	for _, item := range saved.IPs {
		report.IPs = append(report.IPs, ResolvedIP{IP: item.IP, LatencyMS: item.LatencyMS, Reachable: item.Reachable})
	}
	sortResolvedIPs(report.IPs)
	if report.TTLSeconds <= 0 {
		report.TTLSeconds = 300
	}
	now := time.Now()
	expiresAt := time.Unix(report.ResolvedAt, 0).Add(time.Duration(report.TTLSeconds) * time.Second)
	if report.ResolvedAt == 0 {
		report.ResolvedAt = now.Unix()
		expiresAt = now
	}
	r.mu.Lock()
	if _, exists := r.cache[key]; !exists {
		r.cache[key] = &cachedResolution{report: report, expiresAt: expiresAt}
	}
	r.mu.Unlock()
}

func (r *upstreamResolver) persist(target upstreamTarget, report *ResolutionReport) {
	if r.store == nil {
		return
	}
	saved := &store.UpstreamResolution{
		Scheme:       target.Scheme,
		Host:         target.Host,
		Port:         target.Port,
		DurationMS:   report.DurationMS,
		ResolvedAt:   report.ResolvedAt,
		TTLSeconds:   report.TTLSeconds,
		RegionOK:     report.RegionOK,
		RegionFailed: report.RegionFailed,
		Error:        report.Error,
		IPs:          make([]store.UpstreamIP, 0, len(report.IPs)),
	}
	for _, item := range report.IPs {
		saved.IPs = append(saved.IPs, store.UpstreamIP{
			IP:        item.IP,
			LatencyMS: item.LatencyMS,
			Reachable: item.Reachable,
		})
	}
	if err := r.store.SaveUpstreamResolution(saved); err != nil {
		r.logger.Printf("[doh] 持久化解析结果失败 %s: %v", target.Host, err)
	}
}

func (r *upstreamResolver) probeOne(ctx context.Context, target upstreamTarget, ip string) (int64, bool) {
	select {
	case r.probeSem <- struct{}{}:
	case <-ctx.Done():
		return -1, false
	}
	defer func() { <-r.probeSem }()
	if r.probeFn != nil {
		return r.probeFn(ctx, target, ip)
	}

	probeCtx, cancel := context.WithTimeout(ctx, dohProbeTimeout)
	defer cancel()
	addr := net.JoinHostPort(ip, strconv.Itoa(target.Port))
	start := time.Now()

	raw, err := (&net.Dialer{Timeout: dohProbeTimeout}).DialContext(probeCtx, "tcp", addr)
	if err != nil {
		return -1, false
	}
	if target.Scheme == "https" {
		tlsConn := tls.Client(raw, &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         target.Host,
			InsecureSkipVerify: true, // #nosec G402 -- upstream verification is intentionally disabled
		})
		if err := tlsConn.HandshakeContext(probeCtx); err != nil {
			_ = raw.Close()
			return -1, false
		}
		_ = tlsConn.Close()
	} else {
		_ = raw.Close()
	}
	return time.Since(start).Milliseconds(), true
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
