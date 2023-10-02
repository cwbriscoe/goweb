// Copyright 2023 Christopher Briscoe.  All rights reserved.

// Package limiter provides a mechanism to throttle incoming requests
package limiter

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbriscoe/goutil/logging"
	"github.com/cwbriscoe/goutil/net"
	"github.com/cwbriscoe/goweb/tracker"
	"golang.org/x/time/rate"
)

type visitorType uint64

const (
	undefined visitorType = iota
	user
	goodBot
	badBot
)

// visitor contains the rate limit and the last time they visited.
type visitor struct {
	name       string        // name of visitor (botname, ip address, etc)
	limiter    *rate.Limiter // the rate limiter for this visitor
	vtype      visitorType   // type of visitor (user, goodBot, badBot)
	firstSeen  time.Time     // time of first request since last visitor purge
	lastSeen   time.Time     // time of last request
	delayCount uint64        // total number of times this visitor has been delayed
	currDelays int64         // current number of delayed transactions
}

// botEntry stores info for a search/crawler/spider bot
type botEntry struct {
	name string
	host string
}

// Rate stores the rate limit for a class of limiter.
type Rate struct {
	Interval   time.Duration // max interval between requests
	Burst      int           // max number of transactions that can ignore the interval before the limiting begins
	MaxDelayed uint64        // ignored for global rate limiter
}

// LimitSettings contains the global, bot and user rate limit setttings.
type LimitSettings struct {
	Name        string
	Log         *logging.Logger
	GlobalRate  Rate
	GoodBotRate Rate
	UserRate    Rate
}

// Limiter contains variables and resources for a Limiter instance.
type Limiter struct {
	sync.RWMutex
	vars     *LimitSettings
	global   *rate.Limiter // the global limiter if active
	visitors map[string]*visitor
}

type sharedResources struct {
	limiters   []*Limiter           // list of all limiters created
	limitersmu sync.Mutex           // limiters slice mutex
	gbotsmu    sync.RWMutex         // good bots map mutex
	gbots      map[string]*botEntry // good bots map [ip]*botEntry
	bbotsmu    sync.RWMutex         // bad bots mutex
	bbots      map[string]*botEntry // bad bots map [ip]*botEntry
}

// ErrTooManyRequests is returned instead of delaying when the current
// visitor has too many delayed transactions
var ErrTooManyRequests = errors.New("Limiter: Too many current delays")

var shared *sharedResources

// NewLimiter creates a new rate limiter for one or more resources.
func NewLimiter(settings *LimitSettings) (*Limiter, error) {
	if settings.UserRate.Burst <= 0 {
		return nil, errors.New("user rate must have a burst greater than zero")
	}

	if settings.GlobalRate.Burst > 0 && settings.GlobalRate.Interval == 0 {
		settings.GlobalRate.Interval = time.Nanosecond
	}

	if settings.GoodBotRate.Burst > 0 && settings.GoodBotRate.Interval == 0 {
		settings.GoodBotRate.Interval = time.Nanosecond
	}

	// if no good bot rate provided, use the same rate as a regular user
	if settings.GoodBotRate.Burst == 0 && settings.GoodBotRate.Interval == 0 {
		settings.GoodBotRate.Burst = settings.UserRate.Burst
		settings.GoodBotRate.Interval = settings.UserRate.Interval
	}

	limiter := &Limiter{
		vars:     settings,
		visitors: make(map[string]*visitor),
	}

	if limiter.vars.GlobalRate.Burst > 0 {
		limiter.global = rate.NewLimiter(rate.Every(limiter.vars.GlobalRate.Interval), limiter.vars.GlobalRate.Burst)
	}

	limiter.setupSharedResources()

	limiter.vars.Log.Info().Msgf("%s limiter started", limiter.vars.Name)

	return limiter, nil
}

// WriteErrorResponse is a utility function to write the correct http response
// depending on the error return from the Limiter handler.
func WriteErrorResponse(w http.ResponseWriter, err error) {
	if err == ErrTooManyRequests {
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// setupSharedResources sets up global vars and resources to be used by all instances of Limiter.
func (r *Limiter) setupSharedResources() {
	var once sync.Once
	once.Do(func() {
		shared = &sharedResources{
			gbots: make(map[string]*botEntry),
			bbots: make(map[string]*botEntry),
		}
		go shared.daemon()
	})
	shared.limitersmu.Lock()
	defer shared.limitersmu.Unlock()
	shared.limiters = append(shared.limiters, r)
}

func (r *Limiter) getVisitorEntry(ip string) *visitor {
	r.Lock()
	defer r.Unlock()
	visitor, exists := r.visitors[ip]
	if !exists {
		return nil
	}
	visitor.lastSeen = time.Now()
	return visitor
}

func (r *Limiter) createVisitor(ip, name string, typ visitorType) *visitor {
	var interval time.Duration
	var burst int

	switch typ {
	case user:
		interval = r.vars.UserRate.Interval
		burst = r.vars.UserRate.Burst
	case goodBot:
		interval = r.vars.GoodBotRate.Interval
		burst = r.vars.GoodBotRate.Burst
	default:
		interval = 1 * time.Hour
		burst = 1
	}

	limiter := rate.NewLimiter(rate.Every(interval), burst)
	now := time.Now()

	r.Lock()
	defer r.Unlock()

	r.visitors[ip] = &visitor{name, limiter, typ, now, now, 0, 0}
	return r.visitors[ip]
}

func (r *Limiter) getExistingLimiter(ip string) (*rate.Limiter, string) {
	v := r.getVisitorEntry(ip)
	if v != nil {
		return v.limiter, v.name
	}
	return nil, ""
}

func (r *Limiter) logNewVisitor(ip, name string, typ visitorType, info *tracker.Info) {
	var uname string
	if info != nil {
		uname = info.Name
	} else {
		uname = "anon"
	}
	r.vars.Log.Info().Msgf("%s(%d):%s %s: new visitor", ip, typ, uname, name)
}

func (r *Limiter) upgradeIfGoodBot(ip string, info *tracker.Info) (*rate.Limiter, string) {
	isGoodBot, name := isGoodBot(ip)
	if isGoodBot {
		visitor := r.createVisitor(ip, name, goodBot)
		r.logNewVisitor(ip, r.vars.Name, goodBot, info)
		return visitor.limiter, name
	}
	return nil, ""
}

func (r *Limiter) downgradeIfBadBot(ip string, info *tracker.Info) (*rate.Limiter, string) {
	isBadBot, name := isBadBot(ip)
	if isBadBot {
		visitor := r.createVisitor(ip, name, badBot)
		r.logNewVisitor(ip, r.vars.Name, badBot, info)
		return visitor.limiter, name
	}
	return nil, ""
}

func (r *Limiter) getNewLimiter(ip, ua string, info *tracker.Info) (*rate.Limiter, string) {
	gbotLimiter, name := r.upgradeIfGoodBot(ip, info)
	if gbotLimiter != nil {
		return gbotLimiter, name
	}

	bbotLimiter, name := r.downgradeIfBadBot(ip, info)
	if bbotLimiter != nil {
		return bbotLimiter, name
	}

	visitor := r.createVisitor(ip, "", user)
	r.logNewVisitor(ip, r.vars.Name, user, info)

	r.botLookupBackground(ip, ua)

	return visitor.limiter, ""
}

func (r *Limiter) getLimiter(ip, ua string, info *tracker.Info, req *http.Request) *rate.Limiter {
	limiter, name := r.getExistingLimiter(ip)
	if limiter == nil {
		limiter, name = r.getNewLimiter(ip, ua, info)
	}
	if name != "" {
		req.Header.Set("Visitor-Name", name)
	} else {
		if info != nil {
			if info.Auth {
				req.Header.Set("Visitor-Name", info.Name)
			} else {
				req.Header.Set("Visitor-Name", ip+"|"+info.Name)
			}
		} else {
			req.Header.Set("Visitor-Name", ip)
		}
	}
	return limiter
}

func (r *Limiter) globalDelay(ip string, delay time.Duration) {
	r.vars.Log.Info().Msgf("%s %s: globally limited for %s", ip, r.vars.Name, delay.String())
	time.Sleep(delay)
}

func (r *Limiter) visitorDelay(ip string, delay time.Duration) error {
	visitor := r.getVisitorEntry(ip)
	if visitor == nil {
		r.vars.Log.Error().Msgf("getVisitorEntry() returned nil for ip %s", ip)
		return nil
	}

	atomic.AddUint64(&visitor.delayCount, 1)
	atomic.AddInt64(&visitor.currDelays, 1)
	cnt := atomic.LoadUint64(&visitor.delayCount)
	curr := atomic.LoadInt64(&visitor.currDelays)

	var maxDelayed uint64
	switch visitor.vtype {
	case user:
		maxDelayed = r.vars.UserRate.MaxDelayed
	case goodBot:
		maxDelayed = r.vars.GoodBotRate.MaxDelayed
	default:
		maxDelayed = 1
	}

	var err error
	doSleep := true
	if maxDelayed > 0 && curr > int64(maxDelayed) {
		doSleep = false
		err = ErrTooManyRequests
	}

	if err != nil {
		r.vars.Log.Warn().Msgf("%s(%d) %s: exceeded max limit of %d; tot limits = %d", ip, visitor.vtype, r.vars.Name, maxDelayed, cnt)
	} else {
		r.vars.Log.Info().Msgf("%s(%d) %s: limited for %s; tot limits = %d; curr limits = %d", ip, visitor.vtype, r.vars.Name, delay.String(), cnt, curr)
	}

	if doSleep {
		time.Sleep(delay)
	}

	if curr > 0 {
		atomic.AddInt64(&visitor.currDelays, -1)
	}

	return err
}

// limit will limit the ip address based on the configured settings for the resources it limits.
func (r *Limiter) limit(ip string, info *tracker.Info, req *http.Request) error {
	// if no ip is passed, just return
	if ip == "" {
		return errors.New("limiter ip address was empty")
	}

	ua := req.Header.Get("User-Agent")

	// get a limiter for the ip address
	limiter := r.getLimiter(ip, ua, info, req)

	// get a reservation to perform the request
	reservation := limiter.Reserve()

	// see how long we need to delay if at all
	delay := reservation.Delay()
	if delay > 0 {
		if err := r.visitorDelay(ip, delay); err != nil {
			reservation.Cancel()
			return err
		}
	}

	// now do the same delay if there is a global limiter
	if r.global != nil {
		reservation = r.global.Reserve()
		delay = reservation.Delay()
		if delay > 0 {
			r.globalDelay(ip, delay)
		}
	}

	return nil
}

// LimitRequest will get the true ip address from the request and will limit the ip address based
// on the configured settings for the resources it limits.
func (r *Limiter) LimitRequest(w http.ResponseWriter, req *http.Request) error {
	ip := net.GetIP(req)

	info := tracker.GetTrackingInfo(w, req)

	return r.limit(ip, info, req)
}
