// Copyright 2023 Christopher Briscoe.  All rights reserved.

// Package server starts a web server with some preset routes and configuration
package server

import (
	"context"
	"os"
	"time"

	"github.com/cwbriscoe/goutil/compress"
	"github.com/cwbriscoe/goutil/logging"
	"github.com/cwbriscoe/goweb/auth"
	"github.com/cwbriscoe/goweb/config"
	"github.com/cwbriscoe/goweb/limiter"
	"github.com/cwbriscoe/webcache"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/julienschmidt/httprouter"
)

// Server stores configuration for currently running server instance
type Server struct {
	Config     *config.Config
	Router     *httprouter.Router
	DB         *pgxpool.Pool
	Log        *logging.Logger
	Cache      *webcache.WebCache
	GzipPool   *compress.GzipPool
	BrotliPool *compress.BrotliPool
	Limiter    *limiter.Limiter
	auth       *auth.Auth
}

func (s *Server) readConfig() error {
	var err error

	// check for config files distribution folder
	if err = s.Config.Load("./config/" + s.Config.Environment + ".json"); err == nil {
		return nil
	}

	if !os.IsNotExist(err) {
		return err
	}

	// create a blank dev config file if no other ones exist and then panic
	if err = s.Config.Save("./config/dev.json"); err != nil {
		return err
	}

	panic("no config file found.  created default file.")
}

// Init loads the config and sets the server up to be started
func (s *Server) Init() {
	// read config file
	if err := s.readConfig(); err != nil {
		panic(err)
	}

	// create server resources
	s.initSvr()
}

func (s *Server) initSvr() {
	// init gzip and brotli pools
	s.GzipPool = compress.NewGzipPool(6)
	s.BrotliPool = compress.NewBrotliPool(6)

	// init http logger
	var err error
	s.Log, err = logging.NewLogger(logging.Config{
		BaseDir:    s.Config.LogDir,
		FileName:   "server.log",
		MaxAge:     time.Hour * 24 * 30,
		MaxSize:    1024 * 1024,
		MaxBackups: 100,
		Console:    s.Config.LogConsole,
		Compress:   true,
	})
	if err != nil {
		panic(err)
	}

	// init api login
	connstr := "postgresql://" +
		s.Config.DB.Host + ":" +
		s.Config.DB.Port + "/" +
		s.Config.DB.Name + "?user=" +
		s.Config.DB.User + "&password=" +
		s.Config.DB.Pass
	s.DB, err = pgxpool.New(context.Background(), connstr)
	if err != nil {
		panic(err)
	}

	// init cache
	s.Cache = webcache.NewWebCache(s.Config.Cache.Capacity, s.Config.Cache.Buckets)

	// init logger for limiters
	limiterLogger, err := logging.NewLogger(logging.Config{
		BaseDir:    s.Config.LogDir,
		FileName:   "limiter.log",
		MaxAge:     time.Hour * 24 * 30,
		MaxSize:    1024 * 1024,
		MaxBackups: 100,
		Console:    false,
		Compress:   true,
	})
	if err != nil {
		panic(err)
	}

	// init api limiter
	s.Limiter, err = limiter.NewLimiter(
		&limiter.LimitSettings{
			Name: "api",
			Log:  limiterLogger,
			UserRate: limiter.Rate{
				Interval:   time.Second / 2,
				Burst:      3,
				MaxDelayed: 2,
			},
			GoodBotRate: limiter.Rate{
				Interval: 50 * time.Millisecond,
				Burst:    4,
			},
		})
	if err != nil {
		panic(err)
	}

	// init router
	s.Router = httprouter.New()

	var secretPath string
	if s.Config.Environment == "dev" {
		secretPath = "/home/chris/env/webroot/config/secrets.json"
	} else {
		secretPath = "./config/secrets.json"
	}

	// init logger for access
	accessLogger, err := logging.NewLogger(logging.Config{
		BaseDir:    s.Config.LogDir,
		FileName:   "access.log",
		MaxAge:     time.Hour * 24 * 30,
		MaxSize:    1024 * 1024,
		MaxBackups: 100,
		Console:    false,
		Compress:   true,
	})
	if err != nil {
		panic(err)
	}

	// init the auth handlers
	s.auth = auth.NewAuth(&auth.Config{
		Issuer:             s.Config.HTTPS.Domain,
		SecretPath:         secretPath,
		Router:             s.Router,
		AccessExpire:       5 * time.Minute,
		RefreshExpire:      30 * 24 * time.Hour,
		UserRate:           10 * time.Second,
		GlobalRate:         50 * time.Millisecond,
		LimiterLogger:      limiterLogger,
		DB:                 s.DB,
		Log:                accessLogger,
		EnableRegistration: s.Config.Features.EnableRegistration,
	})

	s.initRoutes()
}
