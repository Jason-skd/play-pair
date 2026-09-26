// Package config parses process configuration once, before dependency assembly.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DBMaxConns       int32
	StartupTimeout   time.Duration
	ReadyTimeout     time.Duration
	ShutdownTimeout  time.Duration
	AuthCookieSecure bool
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{HTTPAddr: ":8080", DBMaxConns: 4, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second, ShutdownTimeout: 10 * time.Second, AuthCookieSecure: true}
	if v := getenv("HTTP_ADDR"); v != "" {
		c.HTTPAddr = v
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR: expected host:port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR: invalid port")
	}
	c.DatabaseURL = getenv("DATABASE_URL")
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return Config{}, fmt.Errorf("DATABASE_URL: expected PostgreSQL URL")
	}
	if v := getenv("DB_MAX_CONNS"); v != "" {
		n, e := strconv.ParseInt(v, 10, 32)
		if e != nil || n < 1 || n > 100 {
			return Config{}, fmt.Errorf("DB_MAX_CONNS: expected integer between 1 and 100")
		}
		c.DBMaxConns = int32(n)
	}
	for key, dst := range map[string]*time.Duration{"STARTUP_TIMEOUT": &c.StartupTimeout, "READY_TIMEOUT": &c.ReadyTimeout, "SHUTDOWN_TIMEOUT": &c.ShutdownTimeout} {
		if v := getenv(key); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 || d > time.Minute {
				return Config{}, fmt.Errorf("%s: expected duration in (0, 1m]", key)
			}
			*dst = d
		}
	}
	if v := getenv("AUTH_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("AUTH_COOKIE_SECURE: expected boolean")
		}
		c.AuthCookieSecure = b
	}
	return c, nil
}
