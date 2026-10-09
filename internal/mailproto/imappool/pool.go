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
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"mailhearth/internal/provider"
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
	OrgID int64
	ConnectionID int64
	MailboxID int64
	ConnectionRevision int64
	EndpointRevision int64
	CredentialID int64
	CredentialGeneration int64
	Addr string
	TLSMode string
	TLSConfig *tls.Config
	Dialer *net.Dialer
}

func (c Cred) key() string {
	if c.MailboxID>0 { return fmt.Sprintf("%d/%d/%d/imap/%d/%d/%d/%d",c.OrgID,c.ConnectionID,c.MailboxID,c.ConnectionRevision,c.EndpointRevision,c.CredentialID,c.CredentialGeneration) }
	sum := sha256.Sum256([]byte(c.Pass))
	return c.Addr+"|"+c.TLSMode+"|"+c.User + "|" + hex.EncodeToString(sum[:])
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
	all map[*Conn]struct{}
	connectionUse map[int64]int
	mailboxUse map[int64]int
	capacity chan struct{}
	connectionEpoch map[int64]uint64
	mailboxEpoch map[int64]uint64
	pending map[uint64]pendingDial
	nextDial uint64

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
	broken   atomic.Bool
	closeOnce sync.Once
}

// New creates a pool.
func New(cfg Config) *Pool {
	if cfg.MaxConns <= 0 || cfg.MaxConns>24 {
		cfg.MaxConns = 24
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
		all:map[*Conn]struct{}{},connectionUse:map[int64]int{},mailboxUse:map[int64]int{},capacity:make(chan struct{}),
		connectionEpoch:map[int64]uint64{},mailboxEpoch:map[int64]uint64{},pending:map[uint64]pendingDial{},
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
	addr,mode:=cred.Addr,cred.TLSMode
	if addr==""{addr,mode=p.cfg.Addr,p.cfg.TLSMode}
	host, _, _ := net.SplitHostPort(addr)
	tlsCfg := cred.TLSConfig
	if tlsCfg==nil{tlsCfg=p.cfg.TLSConfig}
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	tlsCfg=tlsCfg.Clone();tlsCfg.ServerName=host;tlsCfg.InsecureSkipVerify=false
	if tlsCfg.MinVersion<tls.VersionTLS12{tlsCfg.MinVersion=tls.VersionTLS12}
	opts := &imapclient.Options{
		TLSConfig:             tlsCfg,
		UnilateralDataHandler: handler,
		Dialer:                cred.Dialer,
	}
	if opts.Dialer==nil{opts.Dialer=&net.Dialer{Timeout:10*time.Second}}
	raw,err:=opts.Dialer.DialContext(ctx,"tcp",addr);if err!=nil{return nil,err}
	closeOnCancel:=context.AfterFunc(ctx,func(){raw.Close()});defer closeOnCancel()
	deadline:=time.Now().Add(30*time.Second);if requested,ok:=ctx.Deadline();ok && requested.Before(deadline){deadline=requested}
	if err:=raw.SetDeadline(deadline);err!=nil{raw.Close();return nil,err}
	var c *imapclient.Client
	switch mode{
	case TLSStart:c,err=imapclient.NewStartTLS(raw,opts)
	case TLSNone:c=imapclient.New(raw,opts)
	case TLSImplicit:
		secure:=tls.Client(raw,tlsCfg)
		if err=secure.HandshakeContext(ctx);err==nil{c=imapclient.New(secure,opts)}
	default:err=provider.Errorf("invalid","IMAP TLS 模式无效")
	}
	if err!=nil{raw.Close();return nil,err}
	if c.Caps().Has(imap.Cap("AUTH=PLAIN")){err=c.Authenticate(sasl.NewPlainClient("",cred.User,cred.Pass))}else if !c.Caps().Has(imap.Cap("LOGINDISABLED")){err=c.Login(cred.User,cred.Pass).Wait()}else{err=provider.Errorf("unsupported_auth_mechanism","IMAP 没有共同认证机制")}
	if err!=nil{c.Close();var response *imap.Error;if errors.As(err,&response) && response.Type==imap.StatusResponseTypeNo && response.Code==imap.ResponseCodeAuthenticationFailed{return nil,&AuthError{Err:err}};return nil,err}
	if ctx.Err()!=nil{c.Close();return nil,ctx.Err()}
	if err:=raw.SetDeadline(time.Time{});err!=nil{c.Close();return nil,err}
	return c,nil
}

// AuthError marks a failed IMAP login (credential rotated or revoked).
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return "imap login failed: " + e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// Get returns a connection for cred, reusing an idle one when possible.
func (p *Pool) Get(ctx context.Context, cred Cred) (*Conn, error) {
	return p.get(ctx,cred,true)
}

// GetFresh 使用独立认证连接，归还时关闭该连接。
func (p *Pool) GetFresh(ctx context.Context,cred Cred) (*Conn,error) {
	return p.get(ctx,cred,false)
}

func (p *Pool) get(ctx context.Context,cred Cred,reuse bool) (*Conn,error) {
	if ctx.Err()!=nil{return nil,ctx.Err()}
	key := cred.key()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	connectionEpoch,mailboxEpoch:=p.connectionEpoch[cred.ConnectionID],p.mailboxEpoch[cred.MailboxID]
	if l := p.idle[key]; reuse && len(l) > 0 {
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

	if err:=p.reserve(ctx,cred,false);err!=nil{return nil,err}
	dialCtx,cancel:=context.WithCancel(ctx)
	p.mu.Lock()
	if p.closed || p.connectionEpoch[cred.ConnectionID]!=connectionEpoch || p.mailboxEpoch[cred.MailboxID]!=mailboxEpoch{p.mu.Unlock();cancel();p.release(cred);return nil,provider.Errorf("revision_conflict","邮箱连接配置已变化")}
	p.nextDial++;dialID:=p.nextDial;p.pending[dialID]=pendingDial{cred:cred,cancel:cancel};p.wg.Add(1);p.mu.Unlock()
	defer func(){cancel();p.mu.Lock();delete(p.pending,dialID);p.mu.Unlock();p.wg.Done()}()
	client, err := p.dial(dialCtx, cred, nil)
	if err != nil {
		p.release(cred)
		return nil, err
	}
	conn:=&Conn{C: client, cred: cred, key: key, pool: p, lastUsed: time.Now()}
	if !reuse{conn.MarkBroken()}
	p.mu.Lock();if p.closed || dialCtx.Err()!=nil || p.connectionEpoch[cred.ConnectionID]!=connectionEpoch || p.mailboxEpoch[cred.MailboxID]!=mailboxEpoch{p.mu.Unlock();conn.close();return nil,provider.Errorf("revision_conflict","邮箱连接配置已变化")};p.all[conn]=struct{}{};p.mu.Unlock()
	return conn, nil
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
	if c.broken.Load() || isClosed(c.C) {
		c.close()
		return
	}
	p.mu.Lock()
	if p.closed || c.broken.Load() || isClosed(c.C) || len(p.idle[c.key]) >= p.cfg.PerCredIdle {
		p.mu.Unlock()
		c.close()
		return
	}
	c.lastUsed = time.Now()
	p.idle[c.key] = append(p.idle[c.key], c)
	p.mu.Unlock()
}

func (c *Conn) close() {
	c.closeOnce.Do(func(){c.broken.Store(true);c.C.Close();c.pool.mu.Lock();delete(c.pool.all,c);c.pool.mu.Unlock();c.pool.release(c.cred)})
}

func (p *Pool) reserve(ctx context.Context,cred Cred,watch bool) error {
	ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
	for {
		p.mu.Lock()
		if p.closed{p.mu.Unlock();return ErrPoolClosed}
		globalMax,connectionMax:=p.cfg.MaxConns,8
		if watch{globalMax-=4;connectionMax-=2}
		if len(p.sem)<globalMax && (cred.ConnectionID==0 || p.connectionUse[cred.ConnectionID]<connectionMax) && (cred.MailboxID==0 || p.mailboxUse[cred.MailboxID]<3){
			p.sem<-struct{}{};p.connectionUse[cred.ConnectionID]++;p.mailboxUse[cred.MailboxID]++;p.mu.Unlock();return nil
		}
		changed:=p.capacity;p.mu.Unlock()
		if watch{return provider.Errorf("notification_capacity_reached","邮件通知连接额度已满")}
		select{case <-changed:case <-ctx.Done():return provider.Errorf("mail_connection_capacity","邮件连接额度已满")}
	}
}

func (p *Pool) release(cred Cred) {
	p.mu.Lock();defer p.mu.Unlock()
	<-p.sem;p.connectionUse[cred.ConnectionID]--;p.mailboxUse[cred.MailboxID]--
	close(p.capacity);p.capacity=make(chan struct{})
}

type pendingDial struct {cred Cred;cancel context.CancelFunc}

func (p *Pool) invalidate(match func(Cred)bool,connectionID,mailboxID int64) {
	p.mu.Lock();var conns []*Conn;var watchers []*watcher
	if connectionID>0{p.connectionEpoch[connectionID]++};if mailboxID>0{p.mailboxEpoch[mailboxID]++}
	for _,dial:=range p.pending{if match(dial.cred){dial.cancel()}}
	for c:=range p.all{if match(c.cred){conns=append(conns,c);delete(p.idle,c.key)}}
	for _,w:=range p.wtch{if match(w.cred){watchers=append(watchers,w)}}
	p.mu.Unlock()
	for _,c:=range conns{c.close()};for _,w:=range watchers{w.shutdown()}
}

func (p *Pool) InvalidateConnection(id int64){p.invalidate(func(c Cred)bool{return c.ConnectionID==id},id,0)}
func (p *Pool) InvalidateMailbox(id int64){p.invalidate(func(c Cred)bool{return c.MailboxID==id},0,id)}

// MarkBroken tells the pool not to reuse this connection.
func (c *Conn) MarkBroken() { c.broken.Store(true) }

// Cred returns the credential used by this connection.
func (c *Conn) Cred() Cred { return c.cred }

// Select ensures mailbox is selected in the requested mode and returns the
// (possibly cached) selection data with an up-to-date message count.
func (c *Conn) Select(ctx context.Context, mailbox string, readOnly bool) (*imap.SelectData, error) {
	if c.selected == mailbox && c.selData != nil && c.readOnly == readOnly {
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
	stop:=context.AfterFunc(ctx,func(){c.broken.Store(true);c.C.Close();close(done)})
	defer func(){if !stop(){<-done}}()
	fn()
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
	for _,dial:=range p.pending{dial.cancel()}
	var all []*Conn
	for c := range p.all {
		all = append(all, c)
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
	client *imapclient.Client
	dialCancel context.CancelFunc
}

// Subscribe starts (or joins) an IDLE watcher for mailbox and returns a
// channel of events plus a cancel function. The channel is closed when the
// watcher shuts down.
func (p *Pool) Subscribe(cred Cred, mailbox string) (<-chan Event, func()) {
	ch,cancel,err:=p.SubscribeChecked(cred,mailbox)
	if err!=nil{failed:=make(chan Event,1);failed<-Event{Mailbox:mailbox,At:time.Now(),Err:err.Error()};close(failed);return failed,func(){}}
	return ch,cancel
}

func (p *Pool) SubscribeChecked(cred Cred, mailbox string) (<-chan Event, func(),error) {
	key := cred.key() + "|" + mailbox
	ch := make(chan Event, 8)
	started:=false
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		close(ch)
		return ch, func() {},ErrPoolClosed
	}
	connectionEpoch,mailboxEpoch:=p.connectionEpoch[cred.ConnectionID],p.mailboxEpoch[cred.MailboxID]
	w := p.wtch[key]
	if w == nil {
		p.mu.Unlock()
		if err:=p.reserve(context.Background(),cred,true);err!=nil{return nil,nil,err}
		p.mu.Lock()
		if p.closed || p.connectionEpoch[cred.ConnectionID]!=connectionEpoch || p.mailboxEpoch[cred.MailboxID]!=mailboxEpoch{p.mu.Unlock();p.release(cred);return nil,nil,provider.Errorf("revision_conflict","邮箱连接配置已变化")}
		if existing:=p.wtch[key];existing!=nil{p.mu.Unlock();p.release(cred);return p.SubscribeChecked(cred,mailbox)}
		w = &watcher{pool: p, cred: cred, mailbox: mailbox, key: key, events: make(chan Event, 16), subs: map[chan Event]struct{}{}, stop: make(chan struct{})}
		p.wtch[key] = w
		p.wg.Add(1)
		started=true
	}
	p.mu.Unlock()
	w.mu.Lock()
	if w.stopped{w.mu.Unlock();if started{go w.run()};return nil,nil,provider.Errorf("notification_capacity_reached","邮件通知连接已经结束")}
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	if started{go w.run()}
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
	},nil
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
	client:=w.client
	dialCancel:=w.dialCancel
	close(w.stop)
	for ch := range w.subs {
		delete(w.subs, ch)
		close(ch)
	}
	w.mu.Unlock()
	if dialCancel!=nil{dialCancel()}
	if client!=nil{client.Close()}
	w.pool.mu.Lock()
	if w.pool.wtch[w.key] == w {
		delete(w.pool.wtch, w.key)
	}
	w.pool.mu.Unlock()
}

func (w *watcher) run() {
	defer w.pool.wg.Done()
	defer w.shutdown()
	defer w.pool.release(w.cred)
	if err:=w.session();err!=nil{w.broadcast(Event{Mailbox:w.mailbox,At:time.Now(),Err:"upstream_failed"})}
}

// session runs one IDLE connection until stop or a connection error.
func (w *watcher) session() error {
	w.mu.Lock();stopped:=w.stopped;w.mu.Unlock();if stopped{return nil}
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
	w.mu.Lock();if w.stopped{w.mu.Unlock();cancel();return nil};w.dialCancel=cancel;w.mu.Unlock()
	client, err := w.pool.dial(ctx, w.cred, handler)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()
	w.mu.Lock();if w.stopped{w.mu.Unlock();return nil};w.client=client;w.mu.Unlock()
	sel, err := client.Select(w.mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return err
	}
	last := sel.NumMessages
	if !client.Caps().Has(imap.CapIdle) {
		// 服务器未提供 IDLE 时使用 45 秒 NOOP 轮询。
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
