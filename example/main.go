package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/cwbriscoe/goutil/compress"
	"github.com/cwbriscoe/goutil/net"
	"github.com/cwbriscoe/goweb/config"
	"github.com/cwbriscoe/goweb/limiter"
	"github.com/cwbriscoe/goweb/server"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*******************************************************************************
MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN MAIN
*******************************************************************************/

type api struct {
	svr *server.Server
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// parse flags
	logToConsole := flag.Bool("console", false, "log output to console as well")
	flag.Parse()

	// create server
	s := &server.Server{}
	s.Config = &config.Config{}
	s.Config.LogConsole = *logToConsole
	s.Init()

	// setup routes
	api := &api{svr: s}
	api.setupRoutes()

	// run server
	return runSvr(s)
}

func runSvr(s *server.Server) error {
	srv := &http.Server{
		Addr:    s.Config.Listen,
		Handler: s.Router,
	}

	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		// We received an interrupt signal, shut down.
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("error closing listeners: %v", err)
		}
	}()

	s.Log.Info().Msg("server starting")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		// Error starting or closing listener:
		log.Printf("error shutting down server: %v", err)
		return err
	}
	s.Log.Info().Msg("server ending")

	return nil
}

// Resources stores the resources to be used in getter functions
type Resources struct {
	DB     *pgxpool.Pool
	Gz     *compress.GzipPool
	Br     *compress.BrotliPool
	Prefix string
}

var resources *Resources

func (a *api) setupRoutes() {
	resources = &Resources{
		DB:     a.svr.DB,
		Gz:     a.svr.GzipPool,
		Br:     a.svr.BrotliPool,
		Prefix: a.svr.Config.URLPrefix,
	}
	// HTML handlers.
	a.svr.Router.HandlerFunc("GET", "/", a.indexPageHandler("index", 5*time.Minute))
}

/*******************************************************************************
LIMITERS LIMITERS LIMITERS LIMITERS LIMITERS LIMITERS LIMITERS LIMITERS LIMITERS
*******************************************************************************/

func (a *api) apiLimiter(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.svr.Limiter.LimitRequest(w, r); err != nil {
			limiter.WriteErrorResponse(w, err)
			return
		}
		f(w, r)
	}
}

/*******************************************************************************
WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB WEB
*******************************************************************************/

func (a *api) indexPageHandler(group string, cacheDuration time.Duration) http.HandlerFunc {
	return a.svr.HandlePanic(a.apiLimiter(a.svr.Logger(a.getIndexPage(group, cacheDuration))))
}

func (a *api) getIndexPage(group string, cacheDuration time.Duration) http.HandlerFunc {
	var once sync.Once
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			index := &WebIndex{}
			index.SetUp(resources)
			err := a.svr.Cache.AddGroup(group, cacheDuration, index)
			if err != nil {
				panic(err)
			}
		})
		w.Header().Add("Content-Type", "text/html")
		net.SetPreferredEncoding(w, r)
		a.svr.Cacher(w, r, group, "index")
	}
}

/*******************************************************************************
GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET GET
*******************************************************************************/

// WebIndex struct stores resources needed to build the main site index
type WebIndex struct {
	db     *pgxpool.Pool
	gz     *compress.GzipPool
	br     *compress.BrotliPool
	prefix string
}

// SetUp sets the DB to be used by the Github API
func (w *WebIndex) SetUp(res *Resources) {
	w.db = res.DB
	w.gz = res.Gz
	w.br = res.Br
	w.prefix = res.Prefix
}

// Get retrieves information about a repo
func (w *WebIndex) Get(_ context.Context, key string) ([]byte, error) {
	resp := "prefix: " + w.prefix + ", key: " + key + "\n<h1>Hello World</h1>\n"
	return []byte(resp), nil
}
