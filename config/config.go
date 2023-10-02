// Copyright 2023 Christopher Briscoe.  All rights reserved.

// Package config loads a config file with settings to start a web server/app
package config

import (
	"fmt"
	"os"

	"github.com/goccy/go-json"
)

type features struct {
	EnableRegistration bool `json:"enableRegistration"`
	EnableLimiters     bool `json:"enableLimiters"`
}

type cache struct {
	Capacity int64 `json:"capacity"`
	Buckets  int   `json:"buckets"`
}

type db struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port string `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

type https struct {
	Scheme     string `json:"scheme"`
	Domain     string `json:"domain"`
	Port       string `json:"port"`
	AppRoot    string `json:"approot"`
	StaticRoot string `json:"staticroot"`
}

// Config store environment information for the currently running app.
type Config struct {
	LogConsole  bool     `json:"-"`
	URLPrefix   string   `json:"-"`
	Environment string   `json:"environment"`
	RootDir     string   `json:"rootdir"`
	LogDir      string   `json:"logdir"`
	Listen      string   `json:"listen"`
	Features    features `json:"features"`
	Cache       cache    `json:"cache"`
	DB          db       `json:"db"`
	HTTPS       https    `json:"https"`
}

// Load loads a config file.
func (c *Config) Load(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	err = json.Unmarshal(data, c)
	if err != nil {
		return err
	}

	// calculate the base host URL
	c.URLPrefix = c.HTTPS.Scheme + "://" + c.HTTPS.Domain
	if c.HTTPS.Port != "80" && c.HTTPS.Port != "443" {
		c.URLPrefix += ":" + c.HTTPS.Port
	}

	// mask password so we can print config
	pass := c.DB.Pass
	c.DB.Pass = "********"

	// print the config out
	data, err = json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))

	// set the passwords back to original values
	c.DB.Pass = pass

	return nil
}

// Save saves a config file.
func (c *Config) Save(file string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile(file, data, 0o600)
	if err != nil {
		return err
	}

	return nil
}
