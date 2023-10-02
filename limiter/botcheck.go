// Copyright 2023 Christopher Briscoe.  All rights reserved.

package limiter

import (
	"net"
	"strings"
	"time"
)

type userAgent struct {
	name string
	text string
}

var validDomains = []string{
	".crawl.baidu.com.",
	".crawl.baidu.jp.",
	".crawl.yahoo.net.",
	".google.com.",
	".googlebot.com.",
	".neevabot.com.",
	".qwant.com.",
	".search.msn.com.",
	".yandex.com.",
	".yandex.net.",
	".yandex.ru.",
	".bot.semrush.com.",
	//".ptld.qwest.net.", // test
	//"localhost",        // test
}

var uaStrings = []userAgent{
	{"Baidu", "baiduspider"},
	{"Bing", "bingbot"},
	{"Google", "googlebot"},
	{"MSN", "msnbot"},
	{"Neeva", "neevabot"},
	{"Qwantify", "qwantify"},
	{"Yahoo", "yahoo!"},
	{"Yandex", "yandexbot"},
	{"Semrush", "semrushbot"},
	//{"Me", "chrome"}, // test
}

func (r *Limiter) botLookupBackground(ip, ua string) {
	go r.routine(ip, ua)
}

func (r *Limiter) checkUserAgent(ip, ua string) (string, bool) {
	ual := strings.ToLower(ua)
	for _, s := range uaStrings {
		if strings.Contains(ual, s.text) {
			r.vars.Log.Info().Msgf("%s(?) ua string bot match: %s", ip, s.text)
			return s.name, true
		}
	}
	return "", false
}

func (r *Limiter) getHostName(ip string) (string, error) {
	host, err := net.LookupAddr(ip)
	if err != nil {
		r.vars.Log.Err(err).Msg("")
		return "", err
	}
	return host[0], nil
}

func (r *Limiter) getHostNameLoop(ip string) (string, error) {
	var host string
	var err error
	retries := 0

	for {
		host, err = r.getHostName(ip)
		if err == nil {
			break
		}
		retries++
		if retries > 3 {
			r.vars.Log.Info().Msgf("%s(?) too many errors, aborting validation", ip)
			return "", err
		}
		time.Sleep(2 * time.Second)
	}
	return host, nil
}

func (r *Limiter) checkHostName(ip, host string) bool {
	for _, s := range validDomains {
		if strings.Contains(host, s) {
			r.vars.Log.Info().Msgf("%s(?) hostname bot match: %s", ip, host)
			return true
		}
	}
	return false
}

func (r *Limiter) validateIPMatch(ip, host string) (bool, string, error) {
	ipCheck, err := net.LookupIP(host)
	if err != nil {
		r.vars.Log.Info().Msgf("%s(?) returned error when trying to LookupIP(host): %s", ip, err.Error())
		return false, "", err
	}
	ip2 := ipCheck[0].String()
	if ip2 == ip {
		r.vars.Log.Info().Msgf("%s(?) ip forward lookup matches: %s", ip, ip)
		return true, ip2, nil
	}
	return false, ip2, nil
}

func (r *Limiter) validateIPMatchLoop(ip, host string) (bool, string, error) {
	retries := 0
	for {
		valid, ip2, err := r.validateIPMatch(ip, host)
		if err == nil {
			return valid, ip2, nil
		}

		if retries > 3 {
			r.vars.Log.Info().Msgf("%s(?) too many errors, aborting validation", ip)
			return false, "", err
		}
		time.Sleep(2 * time.Second)
	}
}

func (r *Limiter) upgradeLimit(ip, host, name string) {
	shared.gbotsmu.Lock()
	defer shared.gbotsmu.Unlock()

	shared.gbots[ip] = &botEntry{name, host}
	visitor := r.createVisitor(ip, name, goodBot)
	r.vars.Log.Info().Msgf("%s(%d) verfied %s Bot", ip, visitor.vtype, name)
}

func (r *Limiter) routine(ip, ua string) {
	name, success := r.checkUserAgent(ip, ua)
	if !success {
		return
	}

	host, err := r.getHostNameLoop(ip)
	if err != nil {
		return
	}

	if !r.checkHostName(ip, host) {
		r.vars.Log.Warn().Msgf("%s(?) ua bot match with unmatched host(%s), possible bad bot", ip, host)
		return
	}

	valid, ip2, err := r.validateIPMatchLoop(ip, host)
	if err != nil {
		return
	}

	if !valid {
		r.vars.Log.Warn().Msgf("%s(?) -> %s -> %s mismatches, possible bad bot", ip, host, ip2)
		return
	}

	r.upgradeLimit(ip, host, name)
}

func isGoodBot(ip string) (bool, string) {
	shared.gbotsmu.RLock()
	defer shared.gbotsmu.RUnlock()
	entry, exists := shared.gbots[ip]
	if exists {
		return true, entry.name
	}
	return false, ""
}

func isBadBot(ip string) (bool, string) {
	shared.bbotsmu.RLock()
	defer shared.bbotsmu.RUnlock()
	entry, exists := shared.bbots[ip]
	if exists {
		return true, entry.name
	}
	return false, ""
}

// GetBotName will look for a good or bad bot and return its name if found
func GetBotName(ip string) string {
	valid, name := isGoodBot(ip)
	if valid {
		return name
	}
	valid, name = isBadBot(ip)
	if valid {
		return name
	}
	return ""
}
