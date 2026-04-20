package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TipoKrewaz/scadt/internal/config"
	"github.com/TipoKrewaz/scadt/internal/models"
	"github.com/TipoKrewaz/scadt/internal/runner"
)


type Store struct {
	mu       sync.RWMutex
	ring     []models.Event
	ringMax  int
	nextID   int64
	file     *os.File
	fileSize int64
	dir      string
	maxBytes int64 // размер JSONL до ротации
	maxDays  int
}

func openStore(dataDir string, ringMax int, retentionDays int) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		ring:     make([]models.Event, 0, ringMax),
		ringMax:  ringMax,
		dir:      dataDir,
		maxBytes: 50 << 20, // 50 MiB per файл
		maxDays:  retentionDays,
	}
	if err := s.openFile(); err != nil {
		return nil, err
	}
	// прочитать хвост последнего JSONL в ring, чтобы UI не был пустым при старте
	_ = s.loadTail()
	return s, nil
}

func (s *Store) openFile() error {
	path := filepath.Join(s.dir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	s.file = f
	s.fileSize = info.Size()
	return nil
}

func (s *Store) loadTail() error {
	path := filepath.Join(s.dir, "events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// naive: читаем весь файл, берём последние ringMax строк
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 128*1024), 4*1024*1024)
	var tail []models.Event
	for sc.Scan() {
		var e models.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil {
			tail = append(tail, e)
			if len(tail) > s.ringMax {
				tail = tail[1:]
			}
		}
	}
	s.mu.Lock()
	s.ring = tail
	if len(tail) > 0 {
		s.nextID = tail[len(tail)-1].ID
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) Append(e models.Event) models.Event {
	s.mu.Lock()
	e.ID = atomic.AddInt64(&s.nextID, 1)
	if e.Fingerprint == "" {
		e.Fingerprint = fingerprint(e.Service, e.Message)
	}
	s.ring = append(s.ring, e)
	if len(s.ring) > s.ringMax {
		s.ring = s.ring[len(s.ring)-s.ringMax:]
	}
	s.mu.Unlock()

	if s.file != nil {
		b, _ := json.Marshal(e)
		b = append(b, '\n')
		n, err := s.file.Write(b)
		if err == nil {
			atomic.AddInt64(&s.fileSize, int64(n))
			if atomic.LoadInt64(&s.fileSize) > s.maxBytes {
				_ = s.rotate()
			}
		} else {
			log.Printf("store: write failed: %v", err)
		}
	}
	return e
}

func (s *Store) rotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	_ = s.file.Close()
	ts := time.Now().UTC().Format("20060102T150405Z")
	src := filepath.Join(s.dir, "events.jsonl")
	dst := filepath.Join(s.dir, "events-"+ts+".jsonl")
	if err := os.Rename(src, dst); err != nil {
		log.Printf("store: rotate rename: %v", err)
	}
	f, err := os.OpenFile(src, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.file = f
	s.fileSize = 0
	go s.gcOld()
	return nil
}

func (s *Store) gcOld() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(s.maxDays) * 24 * time.Hour)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "events-") || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func (s *Store) Recent(limit int) []models.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.ring) {
		limit = len(s.ring)
	}
	out := make([]models.Event, limit)
	copy(out, s.ring[len(s.ring)-limit:])
	return out
}

// Search ищет по ring buffer (rotated JSONL не включаются).
func (s *Store) Search(q models.EventQuery) []models.Event {
	s.mu.RLock()
	all := make([]models.Event, len(s.ring))
	copy(all, s.ring)
	s.mu.RUnlock()

	var rx *regexp.Regexp
	if q.Regex != "" {
		if r, err := regexp.Compile("(?i)" + q.Regex); err == nil {
			rx = r
		}
	}
	var out []models.Event
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if q.Server != "" && e.Server != q.Server {
			continue
		}
		if q.Service != "" && e.Service != q.Service {
			continue
		}
		if q.Level != "" && !levelAtLeast(e.Level, q.Level) {
			continue
		}
		if !q.Since.IsZero() && e.Timestamp.Before(q.Since) {
			continue
		}
		if !q.Until.IsZero() && e.Timestamp.After(q.Until) {
			continue
		}
		if rx != nil && !rx.MatchString(e.Message) && !rx.MatchString(e.Service) {
			continue
		}
		out = append(out, e)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out
}

var levelOrder = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

func levelAtLeast(got, min string) bool { return levelOrder[got] >= levelOrder[min] }

func fingerprint(service, msg string) string {
	// Удаляем переменные части: числа, IP, UUID → чтобы похожие сообщения
	// схлопывались в один fingerprint.
	norm := msg
	norm = reNumber.ReplaceAllString(norm, "<N>")
	norm = reUUID.ReplaceAllString(norm, "<UUID>")
	norm = reIP.ReplaceAllString(norm, "<IP>")
	h := fnv.New64a()
	_, _ = h.Write([]byte(service + "|" + norm))
	return strconv.FormatUint(h.Sum64(), 16)
}

var (
	reNumber = regexp.MustCompile(`\b\d+\b`)
	reUUID   = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reIP     = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
)


type Hub struct {
	mu    sync.RWMutex
	sinks map[chan models.Event]struct{}
}

func newHub() *Hub { return &Hub{sinks: make(map[chan models.Event]struct{})} }

func (h *Hub) Subscribe() chan models.Event {
	ch := make(chan models.Event, 64)
	h.mu.Lock()
	h.sinks[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan models.Event) {
	h.mu.Lock()
	if _, ok := h.sinks[ch]; ok {
		delete(h.sinks, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *Hub) Publish(e models.Event) {
	h.mu.RLock()
	sinks := make([]chan models.Event, 0, len(h.sinks))
	for c := range h.sinks {
		sinks = append(sinks, c)
	}
	h.mu.RUnlock()
	for _, c := range sinks {
		select {
		case c <- e:
		default: // медленный подписчик: дропаем, publisher не блокируем
		}
	}
}


type HealthChecker struct {
	cfg    *config.Store
	client *http.Client
}

func newHealth(cfg *config.Store) *HealthChecker {
	return &HealthChecker{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (h *HealthChecker) Start(ctx context.Context) {
	go h.loop(ctx)
}

func (h *HealthChecker) loop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	// первая итерация сразу
	h.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.checkAll(ctx)
		}
	}
}

func (h *HealthChecker) checkAll(ctx context.Context) {
	snap := h.cfg.Snapshot()
	for i := range snap.Servers {
		srv := snap.Servers[i]
		go h.checkOne(ctx, srv.Name)
	}
}

func (h *HealthChecker) checkOne(ctx context.Context, name string) {
	snap := h.cfg.Snapshot()
	var srv *models.Server
	for i := range snap.Servers {
		if snap.Servers[i].Name == name {
			srv = &snap.Servers[i]
			break
		}
	}
	if srv == nil {
		return
	}
	hc := srv.Health
	if hc == nil {
		hc = &models.HealthCfg{Type: "http", Path: "/", Every: "10s"}
	}

	start := time.Now()
	stat := models.RuntimeStat{LastSeen: start}

	switch strings.ToLower(hc.Type) {
	case "", "http":
		if strings.TrimSpace(srv.URL) == "" {
			stat.State = "offline"
			stat.LastErr = "empty URL"
			break
		}
		u := strings.TrimRight(srv.URL, "/") + "/" + strings.TrimLeft(hc.Path, "/")
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if reqErr != nil || req == nil {
			stat.State = "offline"
			if reqErr != nil {
				stat.LastErr = "bad URL: " + reqErr.Error()
			} else {
				stat.LastErr = "bad URL"
			}
			break
		}
		if srv.Auth != nil {
			applyAuth(req, srv.Auth)
		}
		resp, err := h.client.Do(req)
		if err != nil {
			stat.State = "offline"
			stat.LastErr = err.Error()
		} else {
			resp.Body.Close()
			stat.Ping = int(time.Since(start).Milliseconds())
			if resp.StatusCode >= 500 {
				stat.State = "degraded"
				stat.LastErr = "HTTP " + strconv.Itoa(resp.StatusCode)
			} else if resp.StatusCode >= 400 {
				stat.State = "degraded"
				stat.LastErr = "HTTP " + strconv.Itoa(resp.StatusCode)
			} else {
				stat.State = "online"
			}
		}
	case "tcp":
		host := srv.URL
		// нормализуем URL → host:port
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimSuffix(host, "/")
		if !strings.Contains(host, ":") {
			host += ":80"
		}
		d := net.Dialer{Timeout: 3 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", host)
		if err != nil {
			stat.State = "offline"
			stat.LastErr = err.Error()
		} else {
			_ = conn.Close()
			stat.Ping = int(time.Since(start).Milliseconds())
			stat.State = "online"
		}
	default:
		stat.State = "unknown"
	}

	_ = h.cfg.Update(func(f *config.File) {
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				f.Servers[i].Status = stat
				return
			}
		}
	})
}

func applyAuth(req *http.Request, a *models.Auth) {
	switch a.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+a.Token)
	case "basic":
		req.SetBasicAuth(a.User, a.Pass)
	case "header":
		if a.Name != "" {
			req.Header.Set(a.Name, a.Value)
		}
	}
}


type Driver interface {
	Run(ctx context.Context, publish func(models.Event))
}

type mockDriver struct {
	server string
	rng    *rand.Rand
}

var mockServices = []string{"auth-gateway", "users-db-proxy", "payment-api", "email-worker", "search-index", "notifications", "cdn-edge", "ws-broker"}

var mockMessages = []struct {
	level string
	text  string
}{
	{"error", "JWT signature verification failed"},
	{"error", "Connection pool exhausted [timeout=5000ms]"},
	{"error", "upstream_reset_before_response_started{cb_reset}"},
	{"warn", "Rate limit exceeded for IP 192.168.1.105"},
	{"error", "Failed to resolve SMTP host: smtp.mailtrap.io"},
	{"error", "panic: runtime error: invalid memory address"},
	{"warn", "slow query: SELECT * FROM orders WHERE ... (1.8s)"},
	{"error", "redis: connection refused 127.0.0.1:6379"},
	{"error", "tls: handshake failure: unknown cipher"},
	{"warn", "deprecated API /v1/users — migrate to /v2"},
	{"error", "disk usage above 90% on /var/lib/postgresql"},
	{"error", "context deadline exceeded (Client.Timeout)"},
	{"info", "healthcheck OK, uptime 4h32m"},
}

func (m *mockDriver) Run(ctx context.Context, publish func(models.Event)) {
	t := time.NewTimer(time.Duration(500+m.rng.Intn(1500)) * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		msg := mockMessages[m.rng.Intn(len(mockMessages))]
		publish(models.Event{
			Timestamp: time.Now(),
			Server:    m.server,
			Service:   mockServices[m.rng.Intn(len(mockServices))],
			Level:     msg.level,
			Message:   msg.text,
		})
		t.Reset(time.Duration(1500+m.rng.Intn(4500)) * time.Millisecond)
	}
}

// HTTP-poll driver: ждёт JSON endpoint.
// Ожидаемый формат ответа:
//
//	{ "events": [ { "ts": "...", "level": "...", "service": "...",
//	               "message": "...", "trace": "...", "labels": {...} } ],
//	  "cursor": "opaque-string-for-next-request" }
//
// Если cursor не возвращается, используем time-based дедуп по (service,message,ts).
type httpPollDriver struct {
	server string
	url    string
	auth   *models.Auth
	every  time.Duration
	params map[string]string
	client *http.Client
}

type httpPollResponse struct {
	Events []struct {
		TS      time.Time         `json:"ts"`
		Level   string            `json:"level"`
		Service string            `json:"service"`
		Message string            `json:"message"`
		Trace   string            `json:"trace"`
		Labels  map[string]string `json:"labels"`
	} `json:"events"`
	Cursor string `json:"cursor"`
}

func (d *httpPollDriver) Run(ctx context.Context, publish func(models.Event)) {
	cursor := ""
	seen := make(map[string]time.Time) // дедуп без cursor'а
	t := time.NewTimer(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u := d.url
		if cursor != "" {
			u = addQuery(u, "cursor", cursor)
		} else if last, ok := seen["__last__"]; ok {
			u = addQuery(u, "since", last.UTC().Format(time.RFC3339))
		}
		for k, v := range d.params {
			u = addQuery(u, k, v)
		}
		reqCtx, cancel := context.WithTimeout(ctx, d.every+5*time.Second)
		req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
		if reqErr != nil || req == nil {
			cancel()
			log.Printf("http_poll(%s): bad URL %q: %v", d.server, u, reqErr)
			t.Reset(d.every)
			continue
		}
		if d.auth != nil {
			applyAuth(req, d.auth)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := d.client.Do(req)
		cancel()
		if err != nil {
			log.Printf("http_poll(%s): %v", d.server, err)
			t.Reset(d.every)
			continue
		}
		var body httpPollResponse
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&body); err != nil {
			resp.Body.Close()
			t.Reset(d.every)
			continue
		}
		resp.Body.Close()
		for _, ev := range body.Events {
			ts := ev.TS
			if ts.IsZero() {
				ts = time.Now()
			}
			if body.Cursor == "" {
				key := ev.Service + "|" + ev.Message + "|" + ts.Format(time.RFC3339Nano)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = ts
				seen["__last__"] = ts
				// max 1000 ключей
				if len(seen) > 1000 {
					for k := range seen {
						delete(seen, k)
						if len(seen) < 500 {
							break
						}
					}
				}
			}
			publish(models.Event{
				Timestamp: ts,
				Server:    d.server,
				Service:   ev.Service,
				Level:     firstNonEmpty(ev.Level, "error"),
				Message:   ev.Message,
				Trace:     ev.Trace,
				Labels:    ev.Labels,
			})
		}
		cursor = body.Cursor
		t.Reset(d.every)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func addQuery(u, k, v string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + k + "=" + v
}

// tailDriver читает .jsonl; каждая строка = event.
type tailDriver struct {
	server string
	path   string
}

func (d *tailDriver) Run(ctx context.Context, publish func(models.Event)) {
	var offset int64
	t := time.NewTimer(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		f, err := os.Open(d.path)
		if err != nil {
			t.Reset(2 * time.Second)
			continue
		}
		info, _ := f.Stat()
		if info.Size() < offset {
			offset = 0 // файл ротировали
		}
		_, _ = f.Seek(offset, io.SeekStart)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for sc.Scan() {
			var raw struct {
				TS      time.Time         `json:"ts"`
				Level   string            `json:"level"`
				Service string            `json:"service"`
				Message string            `json:"message"`
				Trace   string            `json:"trace"`
				Labels  map[string]string `json:"labels"`
			}
			if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
				continue
			}
			ts := raw.TS
			if ts.IsZero() {
				ts = time.Now()
			}
			publish(models.Event{
				Timestamp: ts,
				Server:    d.server,
				Service:   raw.Service,
				Level:     firstNonEmpty(raw.Level, "error"),
				Message:   raw.Message,
				Trace:     raw.Trace,
				Labels:    raw.Labels,
			})
		}
		offset, _ = f.Seek(0, io.SeekCurrent)
		f.Close()
		t.Reset(1 * time.Second)
	}
}

func buildDriver(s models.Server) Driver {
	every := parseDurOr(s.Driver.Every, 5*time.Second)
	switch strings.ToLower(s.Driver.Type) {
	case "mock":
		return &mockDriver{server: s.Name, rng: rand.New(rand.NewSource(time.Now().UnixNano() ^ int64(len(s.Name))))}
	case "http_poll":
		return &httpPollDriver{
			server: s.Name,
			url:    s.Driver.URL,
			auth:   s.Auth,
			every:  every,
			params: s.Driver.Params,
			client: &http.Client{Timeout: every + 5*time.Second},
		}
	case "tail_file":
		return &tailDriver{server: s.Name, path: s.Driver.Path}
	case "", "none":
		return nil
	default:
		log.Printf("unknown driver type: %s", s.Driver.Type)
		return nil
	}
}

func parseDurOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// DriverManager управляет жизненным циклом per-server драйверов.
type DriverManager struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
	publish func(models.Event)
}

func newDriverManager(pub func(models.Event)) *DriverManager {
	return &DriverManager{
		running: make(map[string]context.CancelFunc),
		publish: pub,
	}
}

func (dm *DriverManager) Sync(ctx context.Context, servers []models.Server) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	want := make(map[string]models.Server, len(servers))
	for _, s := range servers {
		if strings.ToLower(s.Driver.Type) == "none" || s.Driver.Type == "" {
			continue
		}
		want[s.Name+"|"+driverKey(s.Driver)] = s
	}
	// stop removed / changed
	for key, cancel := range dm.running {
		if _, ok := want[key]; !ok {
			cancel()
			delete(dm.running, key)
		}
	}
	// start new
	for key, s := range want {
		if _, ok := dm.running[key]; ok {
			continue
		}
		d := buildDriver(s)
		if d == nil {
			continue
		}
		dctx, cancel := context.WithCancel(ctx)
		dm.running[key] = cancel
		go d.Run(dctx, dm.publish)
	}
}

func driverKey(d models.DriverCfg) string {
	h := sha1.Sum([]byte(d.Type + "|" + d.URL + "|" + d.Path + "|" + d.Every))
	return hex.EncodeToString(h[:8])
}


type AlertEngine struct {
	cfg     *config.Store
	mu      sync.Mutex
	windows map[string][]time.Time // ruleID → timestamps в окне
	lastFire map[string]time.Time
	history []models.AlertFiring
	client  *http.Client
}

func newAlerts(cfg *config.Store) *AlertEngine {
	return &AlertEngine{
		cfg:      cfg,
		windows:  make(map[string][]time.Time),
		lastFire: make(map[string]time.Time),
		client:   &http.Client{Timeout: 6 * time.Second},
	}
}

func (a *AlertEngine) Consume(ctx context.Context, hub *Hub) {
	ch := hub.Subscribe()
	go func() {
		defer hub.Unsubscribe(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				a.onEvent(ctx, e)
			}
		}
	}()
}

func (a *AlertEngine) onEvent(ctx context.Context, e models.Event) {
	snap := a.cfg.Snapshot()
	now := time.Now()
	for _, rule := range snap.AlertRules {
		if !rule.Enabled {
			continue
		}
		if rule.Server != "" && rule.Server != e.Server {
			continue
		}
		if rule.Service != "" && rule.Service != e.Service {
			continue
		}
		if rule.Level != "" && !levelAtLeast(e.Level, rule.Level) {
			continue
		}
		if rule.Regex != "" {
			r, err := regexp.Compile("(?i)" + rule.Regex)
			if err != nil || !r.MatchString(e.Message) {
				continue
			}
		}
		win := parseDurOr(rule.Window, time.Minute)
		a.mu.Lock()
		tsList := append(a.windows[rule.ID], now)
		cutoff := now.Add(-win)
		// чистим старые
		keep := tsList[:0]
		for _, t := range tsList {
			if t.After(cutoff) {
				keep = append(keep, t)
			}
		}
		a.windows[rule.ID] = keep
		count := len(keep)

		shouldFire := rule.Threshold > 0 && count >= rule.Threshold
		cooldown := parseDurOr(rule.Cooldown, 2*time.Minute)
		if shouldFire {
			if last, ok := a.lastFire[rule.ID]; ok && now.Sub(last) < cooldown {
				shouldFire = false
			}
		}
		if shouldFire {
			a.lastFire[rule.ID] = now
			a.windows[rule.ID] = nil
		}
		a.mu.Unlock()

		if shouldFire {
			a.fire(ctx, snap, rule, count, e)
		}
	}
}

func (a *AlertEngine) fire(ctx context.Context, snap config.File, rule models.AlertRule, count int, sample models.Event) {
	firing := models.AlertFiring{
		RuleID:   rule.ID,
		RuleName: rule.Name,
		At:       time.Now(),
		Count:    count,
		Sample:   sample,
	}
	for _, chName := range rule.Channels {
		var ch *models.AlertChannel
		for i := range snap.AlertChannels {
			if snap.AlertChannels[i].Name == chName {
				ch = &snap.AlertChannels[i]
				break
			}
		}
		if ch == nil {
			firing.Errors = append(firing.Errors, "channel not found: "+chName)
			continue
		}
		if err := a.send(ctx, *ch, rule, count, sample); err != nil {
			firing.Errors = append(firing.Errors, chName+": "+err.Error())
		} else {
			firing.Delivered = append(firing.Delivered, chName)
		}
	}
	a.mu.Lock()
	a.history = append(a.history, firing)
	if len(a.history) > 200 {
		a.history = a.history[len(a.history)-200:]
	}
	a.mu.Unlock()
	log.Printf("alert fired: %s (count=%d) delivered=%v errors=%v", rule.Name, count, firing.Delivered, firing.Errors)
}

func (a *AlertEngine) send(ctx context.Context, ch models.AlertChannel, rule models.AlertRule, count int, sample models.Event) error {
	title := fmt.Sprintf("[scadt] %s: %d events in %s", rule.Name, count, rule.Window)
	text := fmt.Sprintf("Sample: %s · %s · %s\nMessage: %s",
		sample.Server, sample.Service, sample.Level, sample.Message)

	var body []byte
	var err error
	var url string
	switch strings.ToLower(ch.Type) {
	case "slack":
		body, err = json.Marshal(map[string]any{
			"text": title + "\n" + "```" + text + "```",
		})
		url = ch.URL
	case "telegram":
		url = "https://api.telegram.org/bot" + ch.Token + "/sendMessage"
		body, err = json.Marshal(map[string]any{
			"chat_id": ch.ChatID,
			"text":    title + "\n\n" + text,
		})
	case "webhook":
		body, err = json.Marshal(map[string]any{
			"rule":  rule,
			"count": count,
			"event": sample,
			"title": title,
		})
		url = ch.URL
	default:
		return fmt.Errorf("unknown channel type: %s", ch.Type)
	}
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range ch.Headers {
		req.Header.Set(k, v)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (a *AlertEngine) History(limit int) []models.AlertFiring {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit <= 0 || limit > len(a.history) {
		limit = len(a.history)
	}
	out := make([]models.AlertFiring, limit)
	copy(out, a.history[len(a.history)-limit:])
	// новейшие первыми
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}


type API struct {
	cfg    *config.Store
	store  *Store
	hub    *Hub
	alerts *AlertEngine
	dm     *DriverManager
}

func (a *API) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers", a.handleServers)
	mux.HandleFunc("/api/servers/update", a.handleServersUpdate)
	mux.HandleFunc("/api/history", a.handleHistory)
	mux.HandleFunc("/api/events/search", a.handleSearch)
	mux.HandleFunc("/api/events", a.handleEventsSSE)
	mux.HandleFunc("/api/debug", a.handleDebug)
	mux.HandleFunc("/api/saved_requests", a.handleSaved)
	mux.HandleFunc("/api/alert_rules", a.handleAlertRules)
	mux.HandleFunc("/api/alert_channels", a.handleAlertChannels)
	mux.HandleFunc("/api/alert_history", a.handleAlertHistory)
	mux.HandleFunc("/api/runner/exec", a.handleRunnerExec)
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/stats", a.handleStats)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) handleServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cfg.Snapshot().Servers)
}

func (a *API) handleServersUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in []models.Server
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	// сохраняем статус от предыдущих (чтобы не сбрасывать пинг)
	prev := a.cfg.Snapshot().Servers
	for i := range in {
		for _, p := range prev {
			if p.Name == in[i].Name {
				in[i].Status = p.Status
				break
			}
		}
	}
	_ = a.cfg.Update(func(f *config.File) { f.Servers = in })
	a.dm.Sync(r.Context(), in)
	writeJSON(w, http.StatusOK, in)
}

func (a *API) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 300
	}
	writeJSON(w, http.StatusOK, a.store.Recent(limit))
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := models.EventQuery{
		Server:  r.URL.Query().Get("server"),
		Service: r.URL.Query().Get("service"),
		Level:   r.URL.Query().Get("level"),
		Regex:   r.URL.Query().Get("regex"),
	}
	if s := r.URL.Query().Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			q.Since = t
		}
	}
	if s := r.URL.Query().Get("until"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			q.Until = t
		}
	}
	if s := r.URL.Query().Get("limit"); s != "" {
		q.Limit, _ = strconv.Atoi(s)
	}
	writeJSON(w, http.StatusOK, a.store.Search(q))
}

func (a *API) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.hub.Subscribe()
	defer a.hub.Unsubscribe(ch)

	fmt.Fprintf(w, ": connected %s\n\n", time.Now().Format(time.RFC3339))
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			if _, err := fmt.Fprintf(w, "event: error\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (a *API) handleDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req models.DebugRequestLike
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json: " + err.Error()})
		return
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	var base string
	var auth *models.Auth
	for _, s := range a.cfg.Snapshot().Servers {
		if s.Name == req.Server {
			base = s.URL
			auth = s.Auth
			break
		}
	}
	target := req.Path
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		if base == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown server: " + req.Server})
			return
		}
		target = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(req.Path, "/")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, strings.NewReader(req.Body))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "duration_ms": time.Since(start).Milliseconds()})
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if auth != nil && req.Headers["Authorization"] == "" {
		applyAuth(httpReq, auth)
	}
	httpReq.Header.Set("User-Agent", "ScaDT/2.0 (debug)")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "duration_ms": time.Since(start).Milliseconds()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		headers[k] = strings.Join(v, ", ")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      resp.StatusCode,
		"duration_ms": time.Since(start).Milliseconds(),
		"headers":     headers,
		"body":        string(body),
	})
}

func (a *API) handleSaved(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.cfg.Snapshot().SavedRequests)
	case http.MethodPost: // заменить весь список
		var in []models.SavedRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = a.cfg.Update(func(f *config.File) { f.SavedRequests = in })
		writeJSON(w, http.StatusOK, in)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.cfg.Snapshot().AlertRules)
	case http.MethodPost:
		var in []models.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = a.cfg.Update(func(f *config.File) { f.AlertRules = in })
		writeJSON(w, http.StatusOK, in)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleAlertChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// secrets не отдаём, возвращаем redacted
		out := a.cfg.Snapshot().AlertChannels
		for i := range out {
			if out[i].Token != "" {
				out[i].Token = "***"
			}
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var in []models.AlertChannel
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// merge: если приходит token="***", оставляем старый
		prev := a.cfg.Snapshot().AlertChannels
		for i := range in {
			if in[i].Token == "***" {
				for _, p := range prev {
					if p.Name == in[i].Name {
						in[i].Token = p.Token
					}
				}
			}
		}
		_ = a.cfg.Update(func(f *config.File) { f.AlertChannels = in })
		writeJSON(w, http.StatusOK, len(in))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.alerts.History(50))
}

func (a *API) handleRunnerExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in models.CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, models.CommandResponse{Error: err.Error()})
		return
	}
	var sshCfg *models.SSHConfig
	for _, s := range a.cfg.Snapshot().Servers {
		if s.Name == in.Server {
			sshCfg = s.SSH
			break
		}
	}
	if sshCfg == nil {
		writeJSON(w, http.StatusBadRequest, models.CommandResponse{Error: "no SSH config for server: " + in.Server})
		return
	}
	timeout := parseDurOr(in.Timeout, 10*time.Second)
	res, err := runner.Exec(r.Context(), sshCfg, in.Command, timeout)
	if err != nil {
		writeJSON(w, http.StatusOK, models.CommandResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, models.CommandResponse{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration.Milliseconds(),
	})
}

func (a *API) handleConfig(w http.ResponseWriter, r *http.Request) {
	snap := a.cfg.Snapshot()
	// не отдаём secrets в /api/config
	for i := range snap.AlertChannels {
		if snap.AlertChannels[i].Token != "" {
			snap.AlertChannels[i].Token = "***"
		}
	}
	for i := range snap.Servers {
		if snap.Servers[i].SSH != nil {
			snap.Servers[i].SSH.Password = ""
			snap.Servers[i].SSH.KeyPass = ""
		}
		if snap.Servers[i].Auth != nil && snap.Servers[i].Auth.Token != "" {
			snap.Servers[i].Auth.Token = "***"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path": a.cfg.Path(),
		"config":      snap,
	})
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	// агрегат по ring buffer: per-server / per-service / per-level
	events := a.store.Recent(2000)
	perServer := map[string]int{}
	perService := map[string]int{}
	perLevel := map[string]int{}
	fp := map[string]struct {
		Count   int          `json:"count"`
		Sample  models.Event `json:"sample"`
		LastSeen time.Time   `json:"last_seen"`
	}{}
	for _, e := range events {
		perServer[e.Server]++
		perService[e.Service]++
		perLevel[e.Level]++
		v := fp[e.Fingerprint]
		v.Count++
		if v.LastSeen.Before(e.Timestamp) {
			v.LastSeen = e.Timestamp
			v.Sample = e
		}
		fp[e.Fingerprint] = v
	}
	// top-20 fingerprints by count
	type fpItem struct {
		Fingerprint string       `json:"fingerprint"`
		Count       int          `json:"count"`
		Sample      models.Event `json:"sample"`
		LastSeen    time.Time    `json:"last_seen"`
	}
	items := make([]fpItem, 0, len(fp))
	for k, v := range fp {
		items = append(items, fpItem{k, v.Count, v.Sample, v.LastSeen})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Count > items[j].Count })
	if len(items) > 20 {
		items = items[:20]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":       len(events),
		"per_server":  perServer,
		"per_service": perService,
		"per_level":   perLevel,
		"top_groups":  items,
	})
}


func doDebugHTTP(ctx context.Context, method, url string, headers map[string]string, auth *models.Auth) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return 0, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if auth != nil && headers["Authorization"] == "" {
		applyAuth(req, auth)
	}
	req.Header.Set("User-Agent", "ScaDT/2.0 (debug)")
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx2)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(body), nil
}


func main() {
	addr := flag.String("addr", "", "listen address (overrides config; только с -headless)")
	configPath := flag.String("config", "scadt.json", "path to config file")
	headless := flag.Bool("headless", false, "HTTP API only, без GUI (CI/скрипты)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	snap := cfg.Snapshot()
	listen := snap.Listen
	if *addr != "" {
		listen = *addr
	}

	dataDir := snap.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(filepath.Dir(*configPath), dataDir)
	}
	store, err := openStore(dataDir, 2000, snap.RetentionDays)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer store.Close()

	hub := newHub()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// publisher: в hub + в store
	publish := func(e models.Event) {
		e = store.Append(e)
		hub.Publish(e)
	}

	dm := newDriverManager(publish)
	dm.Sync(ctx, snap.Servers)

	health := newHealth(cfg)
	health.Start(ctx)

	alerts := newAlerts(cfg)
	alerts.Consume(ctx, hub)

	be := &backend{cfg: cfg, store: store, hub: hub, dm: dm, alerts: alerts}

	if *headless {
		// headless: HTTP API без GUI
		api := &API{cfg: cfg, store: store, hub: hub, alerts: alerts, dm: dm}
		mux := http.NewServeMux()
		api.routes(mux)
		if listen == "" {
			listen = "127.0.0.1:0"
		}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		url := fmt.Sprintf("http://%s/", ln.Addr().String())
		log.Printf("ScaDT headless on %s (config: %s)", url, cfg.Path())
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("serve: %v", err)
			}
		}()
		<-ctx.Done()
		log.Println("shutting down...")
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
		return
	}

	log.Printf("scadt gui: config=%s data=%s", cfg.Path(), dataDir)
	runGUI(be)
	cancel()
}
