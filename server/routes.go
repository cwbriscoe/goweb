// Copyright 2023 Christopher Briscoe.  All rights reserved.

package server

import (
	"time"
)

func (s *Server) initRoutes() {
	// Static Assets
	s.Router.HandlerFunc("GET", "/app/*file", s.appRootHandler("app", 365*24*time.Hour))
	s.Router.HandlerFunc("GET", "/favicon.svg", s.appRootHandler("favicon.svg", 365*24*time.Hour))
	s.Router.HandlerFunc("GET", "/favicon.ico", s.appRootHandler("favicon.ico", 365*24*time.Hour))
	s.Router.HandlerFunc("GET", "/admin/:func/", s.adminHandler())

	// Sitemaps
	s.Router.HandlerFunc("GET", "/sitemap.xml", s.staticHandler("sitemap_index", 6*time.Hour))
	s.Router.HandlerFunc("GET", "/sitemaps/:file", s.staticHandler("sitemaps", 6*time.Hour))
}
