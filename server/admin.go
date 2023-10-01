package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/cwbriscoe/goutil/compress"
	"github.com/cwbriscoe/webcache"
	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Server) adminHandler() http.HandlerFunc {
	return s.HandlePanic(s.Logger(s.auth.AuthHandler("admin", s.getAdminData())))
}

func (s *Server) getAdminData() http.HandlerFunc {
	var once sync.Once
	admin := &Admin{}
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			admin.SetResources(s.DB, s.Cache)
		})
		bytes, err := admin.GetCache(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "application/json")
		w.Header().Add("Content-Encoding", "br")

		_, err = w.Write(bytes)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// Admin struct stores resources needed by the API
type Admin struct {
	db    *pgxpool.Pool
	cache *webcache.WebCache
}

// SetResources sets the DB to be used by the Github API
func (a *Admin) SetResources(db *pgxpool.Pool, cache *webcache.WebCache) {
	a.db = db
	a.cache = cache
}

// GetCache retrieves stats from the cache
func (a *Admin) GetCache(_ context.Context) ([]byte, error) {
	stats := a.cache.BucketStats()

	src, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return []byte(err.Error()), err
	}

	dest, err := compress.Brotli(src, 6)
	if err != nil {
		return nil, err
	}

	return dest, nil
}
