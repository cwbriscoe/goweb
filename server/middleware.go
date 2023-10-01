package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cwbriscoe/goutil/net"
	"github.com/cwbriscoe/goweb/limiter"
	"github.com/cwbriscoe/webcache"
)

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{w, http.StatusOK}
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// HandlePanic will recover and log a panic.
func (s *Server) HandlePanic(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if i := recover(); i != nil {
				s.Log.Error().Msgf("panic(recovered) at %s: %v", r.URL.Path, i)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		f(w, r)
	}
}

// Logger writes request info to the configured log file.
func (s *Server) Logger(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := newLoggingResponseWriter(w)
		f(lrw, r)

		name := r.Header.Get("Visitor-Name")
		if name == "" {
			ip := net.GetIP(r)
			name = limiter.GetBotName(ip)
			if name == "" {
				name = ip
			}
		}

		elapsed := time.Since(start)
		s.Log.Info().Msgf("%d %s %s %v %v", lrw.statusCode, name, r.Method, r.URL, elapsed)
	}
}

func addMaxAgeHeader(w http.ResponseWriter, expires time.Time) {
	maxage := time.Until(expires)
	// set a max maxage of 1 day if it greater.
	if maxage > time.Hour*24 {
		maxage = time.Hour * 24
	}
	if maxage > 0 {
		// if content is html, set a max age of 60 seconds since the html points
		// to the json which hashed filenames will change when there is a code update.
		content := w.Header().Get("Content-Type")
		if content == "text/html" {
			maxage = 60
		} else {
			// if not html, convert the duration to seconds.
			maxage /= time.Second
		}
		w.Header().Add("Cache-Control", "max-age="+strconv.Itoa(int(maxage)))
	}
}

func addCacheMetaHeaders(w http.ResponseWriter, group, key string, info *webcache.CacheInfo) {
	cost := (float64(info.Cost/time.Microsecond) / 1000.0)
	w.Header().Add("Cache-Meta-Group", group)
	w.Header().Add("Cache-Meta-Key", key)
	w.Header().Add("Cache-Meta-Expires", info.Expires.Format(time.RFC3339))
	w.Header().Add("Cache-Meta-Cost", strconv.FormatFloat(cost, 'f', 2, 64))
}

// Cacher stores and retrieves assets from the cache.
func (s *Server) Cacher(w http.ResponseWriter, r *http.Request, group, key string) {
	encoding := w.Header().Get("Content-Encoding")
	switch encoding {
	case "br":
		key += "|br"
	case "gzip":
		key += "|gz"
	}

	match := r.Header.Get("If-None-Match")
	bytes, info, err := s.Cache.Get(r.Context(), group, key, match)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.Log.Err(err).Msgf("group: %s, key: %s", group, key)
		return
	}

	// info should never be null since we should have added all cache groups in startup logic.
	if info == nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.Log.Err(errors.New("null info returned from Cache.Get()")).Msgf("group: %s, key: %s", group, key)
		return
	}

	// if no etag hit and no data is returned from the api, treat it as a 404.
	if bytes == nil && match != info.Etag {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// add headers.
	w.Header().Add("ETag", info.Etag)
	addMaxAgeHeader(w, info.Expires)
	addCacheMetaHeaders(w, group, key, info)

	// if etags match, set 304 header and return.
	if match == info.Etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Add("Content-Length", strconv.Itoa(len(bytes)))

	if _, err = w.Write(bytes); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.Log.Err(err).Msg("error writing to http.ResponseWriter")
	}
}
