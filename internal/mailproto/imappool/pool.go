// Package imappool keeps a small, bounded set of IMAP connections per
// credential and multiplexes IDLE notifications so that many browser tabs
// share one server-side connection. Bounds are deliberate: the product
// targets hosts with a few hundred megabytes of memory.
package imappool

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// TLS modes (mirrors config.TLSMode).
const (
	TLSImplicit = "tls"
	TLSStart    = "starttls"
	TLSNone     = "none"
)

// Cred identifies a mailbox login.
type Cred struct {
	User string
	Pass string
}

func (c Cred) key() string {
	sum := sha256.Sum256([]byte(c.Pass))
	return strings.ToLower(c.User) + "|" + hex.EncodeToString(sum[:8])
}

// Config configures the pool.
type Config struct {
	Addr        string
	TLSMode     string
	TLSConfig   *tls.Config
	MaxConns    int           // hard cap on simultaneously open connections
	PerCredIdle int           // idle connections kept per credential
	IdleTimeout time.Duration // idle connections older than this are closed
	Logger      *slog.Logger
}

// ErrPoolClosed is returned after Close.
var ErrPoolClosed = errors.New("imap pool closed")

// Pool is the connection pool.
type Pool struct {
	cfg  Config
	log  *slog.Logger
	sem  chan struct{}
	mu   sync.Mutex
	idle map[string][]*Conn
	wtch map[string]*watcher

	closed bool
	stop   chan struct{}
	wg     sync.WaitGroup
}

// Conn is a pooled IMAP connection. Callers must return it with Put.
type Conn struct {
	C        *imapclient.Client
	cred     Cred
	key      string
	pool     *Pool
	selected string
	readOnly bool
	selData  *imap.SelectData
	lastUsed time.Time
	broken   bool
}

// New creates a pool.
func New(cfg Config) *Pool {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 16
	}
	if cfg.PerCredIdle <= 0 {
		cfg.PerCredIdle = 2
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 90 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	p := &Pool{
		cfg:  cfg,
		log:  cfg.Logger,
		sem:  make(chan struct{}, cfg.MaxConns),
		idle: map[string][]*Conn{},
		wtch: map[string]*watcher{},
		stop: make(chan struct{}),
	}
	p.wg.Add(1)
	go p.reaper()
	return p
}

// Stats reports pool occupancy (for the admin health page).
func (p *Pool) Stats() (open, idle, watchers int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.idle {
		idle += len(l)
	}
	return len(p.sem), idle, len(p.wtch)
}

func (p *Pool) dial(ctx context.Context, cred Cred, handler *imapclient.UnilateralDataHandler) (*imapclient.Client, error) {
	host, _, _ := net.SplitHostPort(p.cfg.Addr)
	tlsCfg := p.cfg.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	opts := &imapclient.Options{
		TLSConfig:             tlsCfg,
		UnilateralDataHandler: handler,
		Dialer:                &net.Dialer{Timeout: 20 * time.Second},
	}
	var (
		c   *imapclient.Client
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		switch p.cfg.TLSMode {
		case TLSStart:
			c, err = imapclient.DialStartTLS(p.cfg.Addr, opts)
		case TLSNone:
			c, err = imapclient.DialInsecure(p.cfg.Addr, opts)
		default:
			c, err = imapclient.DialTLS(p.cfg.Addr, opts)
		}
		if err != nil {
			return
		}
		if lerr := c.Login(cred.User, cred.Pass).Wait(); lerr != nil {
			c.Close()
			c, err = nil, &AuthError{Err: lerr}
		}
	}()
	select {
	case <-done:
		return c, err
	case <-ctx.Done():
		go func() {
			<-done
			if c != nil {
				c.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// AuthError marks a failed IMAP login (credential rotated or revoked).
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return "imap login failed: " + e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// Get returns a connection for cred, reusing an idle one when possible.
func (p *Pool) Get(ctx context.Context, cred Cred) (*Conn, error) {
	key := cred.key()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	if l := p.idle[key]; len(l) > 0 {
		c := l[len(l)-1]
		p.idle[key] = l[:len(l)-1]
		p.mu.Unlock()
		if c.C.State() == imap.ConnStateLogout || isClosed(c.C) {
			c.close()
			return p.Get(ctx, cred)
		}
		return c, nil
	}
	p.mu.Unlock()

	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	client, err := p.dial(ctx, cred, nil)
	if err != nil {
		<-p.sem
		return nil, err
	}
	return &Conn{C: client, cred: cred, key: key, pool: p, lastUsed: time.Now()}, nil
}

func isClosed(c *imapclient.Client) bool {
	select {
	case <-c.Closed():
		return true
	default:
		return false
	}
}

// Put returns the connection to the pool (or closes it when broken).
func (p *Pool) Put(c *Conn) {
	if c == nil {
		return
	}
	if c.broken || isClosed(c.C) {
		c.close()
		return
	}
	p.mu.Lock()
	if p.closed || len(p.idle[c.key]) >= p.cfg.PerCredIdle {
		p.mu.Unlock()
		c.close()
		return
	}
	c.lastUsed = time.Now()
	p.idle[c.key] = append(p.idle[c.key], c)
	p.mu.Unlock()
}

func (c *Conn) close() {
	c.C.Close()
	<-c.pool.sem
}

// MarkBroken tells the pool not to reuse this connection.
func (c *Conn) MarkBroken() { c.broken = true }

// Cred returns the credential used by this connection.
func (c *Conn) Cred() Cred { return c.cred }

// Select ensures mailbox is selected in the requested mode and returns the
// (possibly cached) selection data with an up-to-date message count.
func (c *Conn) Select(ctx context.Context, mailbox string, readOnly bool) (*imap.SelectData, error) {
	if c.selected == mailbox && c.selData != nil && (c.readOnly == readOnly || !readOnly) {
		if mb := c.C.Mailbox(); mb != nil {
			c.selData.NumMessages = mb.NumMessages
		}
		return c.selData, nil
	}
	var (
		data *imap.SelectData
		err  error
	)
	c.run(ctx, func() {
		data, err = c.C.Select(mailbox, &imap.SelectOptions{ReadOnly: readOnly}).Wait()
	})
	if err != nil {
		c.selected, c.selData = "", nil
		return nil, err
	}
	c.selected, c.readOnly, c.selData = mailbox, readOnly, data
	return data, nil
}

// Invalidate forgets the selected mailbox (after CREATE/DELETE/RENAME).
func (c *Conn) Invalidate() {
	c.selected, c.selData = "", nil
}

// Run executes fn, closing the connection if ctx expires first so that a
// hung server cannot pin a goroutine forever.
func (c *Conn) Run(ctx context.Context, fn func()) { c.run(ctx, fn) }

func (c *Conn) run(ctx context.Context, fn func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			c.broken = true
			c.C.Close()
		}
	}()
	fn()
	close(done)
}

func (p *Pool) reaper() {
	defer p.wg.Done()
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			var victims []*Conn
			p.mu.Lock()
			cutoff := time.Now().Add(-p.cfg.IdleTimeout)
			for k, l := range p.idle {
				keep := l[:0]
				for _, c := range l {
					if c.lastUsed.Before(cutoff) || isClosed(c.C) {
						victims = append(victims, c)
					} else {
						keep = append(keep, c)
					}
				}
				if len(keep) == 0 {
					delete(p.idle, k)
				} else {
					p.idle[k] = keep
				}
			}
			p.mu.Unlock()
			for _, c := range victims {
				go func(c *Conn) {
					c.C.Logout().Wait()
					c.close()
				}(c)
			}
		}
	}
}

// Close shuts the pool down.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.stop)
	var all []*Conn
	for _, l := range p.idle {
		all = append(all, l...)
	}
	p.idle = map[string][]*Conn{}
	ws := make([]*watcher, 0, len(p.wtch))
	for _, w := range p.wtch {
		ws = append(ws, w)
	}
	p.mu.Unlock()
	for _, c := range all {
		c.close()
	}
	for _, w := range ws {
		w.shutdown()
	}
	p.wg.Wait()
}

// --- IDLE watchers ---

// Event describes a change noticed on a watched mailbox.
type Event struct {
	Mailbox     string    `json:"mailbox"`
	NumMessages uint32    `json:"numMessages"`
	Expunged    bool      `json:"expunged"`
	At          time.Time `json:"at"`
	Err         string    `json:"err,omitempty"`
}

type watcher struct {
	pool    *Pool
	cred    Cred
	mailbox string
	key     string
	events  chan Event
	mu      sync.Mutex
	subs    map[chan Event]struct{}
	stop    chan struct{}
	stopped bool
}

// Subscribe starts (or joins) an IDLE watcher for mailbox and returns a
// channel of events plus a cancel function. The channel is closed when the
// watcher shuts down.
func (p *Pool) Subscribe(cred Cred, mailbox string) (<-chan Event, func()) {
	key := cred.key() + "|" + mailbox
	ch := make(chan Event, 8)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	w := p.wtch[key]
	if w == nil {
		w = &watcher{pool: p, cred: cred, mailbox: mailbox, key: key, events: make(chan Event, 16), subs: map[chan Event]struct{}{}, stop: make(chan struct{})}
		p.wtch[key] = w
		p.wg.Add(1)
		go w.run()
	}
	p.mu.Unlock()
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		if _, ok := w.subs[ch]; ok {
			delete(w.subs, ch)
			close(ch)
		}
		empty := len(w.subs) == 0
		w.mu.Unlock()
		if empty {
			time.AfterFunc(15*time.Second, func() {
				w.mu.Lock()
				still := len(w.subs) == 0
				w.mu.Unlock()
				if still {
					w.shutdown()
				}
			})
		}
	}
}

func (w *watcher) broadcast(ev Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (w *watcher) shutdown() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	close(w.stop)
	for ch := range w.subs {
		delete(w.subs, ch)
		close(ch)
	}
	w.mu.Unlock()
	w.pool.mu.Lock()
	if w.pool.wtch[w.key] == w {
		delete(w.pool.wtch, w.key)
	}
	w.pool.mu.Unlock()
}

func (w *watcher) run() {
	defer w.pool.wg.Done()
	defer w.shutdown()
	backoff := 2 * time.Second
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		err := w.session()
		if err == nil {
			return
		}
		var ae *AuthError
		if errors.As(err, &ae) {
			w.broadcast(Event{Mailbox: w.mailbox, At: time.Now(), Err: "auth"})
			return
		}
		w.pool.log.Debug("imap watcher reconnect", "mailbox", w.mailbox, "err", err)
		select {
		case <-w.stop:
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// session runs one IDLE connection until stop or a connection error.
func (w *watcher) session() error {
	select {
	case w.pool.sem <- struct{}{}:
	case <-w.stop:
		return nil
	}
	defer func() { <-w.pool.sem }()

	notify := make(chan Event, 16)
	handler := &imapclient.UnilateralDataHandler{
		Mailbox: func(data *imapclient.UnilateralDataMailbox) {
			if data.NumMessages != nil {
				select {
				case notify <- Event{Mailbox: w.mailbox, NumMessages: *data.NumMessages, At: time.Now()}:
				default:
				}
			}
		},
		Expunge: func(seqNum uint32) {
			select {
			case notify <- Event{Mailbox: w.mailbox, Expunged: true, At: time.Now()}:
			default:
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	client, err := w.pool.dial(ctx, w.cred, handler)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()
	sel, err := client.Select(w.mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return err
	}
	last := sel.NumMessages
	if !client.Caps().Has(imap.CapIdle) {
		// Fallback: poll with NOOP.
		t := time.NewTicker(45 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return nil
			case <-client.Closed():
				return errors.New("connection closed")
			case ev := <-notify:
				w.broadcast(ev)
			case <-t.C:
				if err := client.Noop().Wait(); err != nil {
					return err
				}
				if mb := client.Mailbox(); mb != nil && mb.NumMessages != last {
					last = mb.NumMessages
					w.broadcast(Event{Mailbox: w.mailbox, NumMessages: last, At: time.Now()})
				}
			}
		}
	}
	idle, err := client.Idle()
	if err != nil {
		return err
	}
	for {
		select {
		case <-w.stop:
			idle.Close()
			client.Logout().Wait()
			return nil
		case <-client.Closed():
			return errors.New("connection closed")
		case ev := <-notify:
			if ev.NumMessages == 0 && !ev.Expunged {
				ev.NumMessages = last
			} else if !ev.Expunged {
				last = ev.NumMessages
			}
			w.broadcast(ev)
		}
	}
}
