// Copyright 2023 Christopher Briscoe.  All rights reserved.

package limiter

import "time"

func (s *sharedResources) daemon() {
	for {
		time.Sleep(10 * time.Minute)
		s.trimVisitors()
	}
}

func (*sharedResources) trim(limiter *Limiter) {
	var cnt, total int
	now := time.Now()
	limiter.Lock()
	defer limiter.Unlock()
	for k, v := range limiter.visitors {
		total++
		if now.Sub(v.lastSeen) > time.Hour {
			delete(limiter.visitors, k)
			cnt++
		}
	}
	if cnt > 0 {
		limiter.vars.Log.Info().Msgf("daemon: %s: %d/%d visitors trimmed", limiter.vars.Name, cnt, total)
	}
}

func (s *sharedResources) trimVisitors() {
	s.limitersmu.Lock()
	defer s.limitersmu.Unlock()
	for _, limiter := range s.limiters {
		s.trim(limiter)
	}
}
