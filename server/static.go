package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/cwbriscoe/goutil/compress"
	"github.com/cwbriscoe/goutil/net"
)

// StaticData stores the root path for static and root handlers
type StaticData struct {
	root string
	gz   *compress.GzipPool
	br   *compress.BrotliPool
}

func (s *Server) appRootHandler(group string, cacheDuration time.Duration) http.HandlerFunc {
	return s.Logger(s.getStaticData(group, s.Config.RootDir+s.Config.HTTPS.AppRoot, cacheDuration))
}

func (s *Server) staticHandler(group string, cacheDuration time.Duration) http.HandlerFunc {
	return s.Logger(s.getStaticData(group, s.Config.RootDir+s.Config.HTTPS.StaticRoot, cacheDuration))
}

func (s *Server) getStaticData(group, root string, cacheDuration time.Duration) http.HandlerFunc {
	var once sync.Once
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			static := &StaticData{}
			static.root = root
			static.gz = s.GzipPool
			static.br = s.BrotliPool
			err := s.Cache.AddGroup(group, cacheDuration, static)
			if err != nil {
				panic(err)
			}
		})

		s.processStaticRequest(w, r, group)
	}
}

//revive:disable:cyclomatic
//revive:disable:cognitive-complexity
func (s *Server) processStaticRequest(w http.ResponseWriter, r *http.Request, group string) {
	file := r.URL.Path

	ext := path.Ext(file)
	if ext == "" {
		ext = ".html"
	}

	if ext != ".jpg" && ext != ".png" && ext != ".svg" && ext != ".html" && ext != ".css" &&
		ext != ".js" && ext != ".json" && ext != ".xml" && ext != ".ico" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// debug
	header := r.Header.Get("Accept-Encoding")
	encodings := strings.Split(header, ", ")
	br := false
	gzip := false
	for _, s := range encodings {
		if s == "br" {
			br = true
		}
		if s == "gzip" {
			gzip = true
		}
	}
	if !br || !gzip {
		s.Log.Debug().Msgf("request accept-encoding: %s: %v", file, encodings)
	}
	// end-debug

	switch ext {
	case ".jpg":
		w.Header().Add("Content-Type", "image/jpeg")
	case ".png":
		w.Header().Add("Content-Type", "image/png")
	case ".svg":
		w.Header().Add("Content-Type", "image/svg+xml")
	case ".ico":
		w.Header().Add("Content-Type", "image/x-icon")
	case ".html":
		w.Header().Add("Content-Type", "text/html")
	case ".css":
		w.Header().Add("Content-Type", "text/css")
	case ".js":
		w.Header().Add("Content-Type", "application/javascript")
	case ".json":
		w.Header().Add("Content-Type", "application/json")
	case ".xml":
		w.Header().Add("Content-Type", "application/xml")
	default:
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if ext != ".jpg" && ext != ".png" {
		net.SetPreferredEncoding(w, r)
	}

	s.Cacher(w, r, group, file)
}

//revive:enable:cyclomatic
//revive:enable:cognitive-complexity

// Get loads static data when not found in the cache
func (s *StaticData) Get(_ context.Context, key string) ([]byte, error) {
	keys, encoding := net.GetRequestParams(key)
	file := s.root
	if keys[0] == "" {
		file += "/index.html"
	} else {
		file += keys[0]
	}

	src, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	ext := path.Ext(keys[0])

	if ext == ".jpg" || ext == ".png" {
		return src, nil
	}

	var dest []byte

	if encoding == "br" {
		dest, err = s.br.Compress(src)
		if err != nil {
			return nil, err
		}
	} else {
		dest, err = s.gz.Compress(src)
		if err != nil {
			return nil, err
		}
	}

	return dest, nil
}
