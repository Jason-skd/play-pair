package config

import (
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestLoad(t *testing.T) {
	c, err := Load(env(map[string]string{"DATABASE_URL": "postgres://user:secret@localhost:5432/db"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.DBMaxConns != 4 || c.StartupTimeout != 5*time.Second || c.ReadyTimeout != time.Second || c.ShutdownTimeout != 10*time.Second || !c.AuthCookieSecure {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	insecure, err := Load(env(map[string]string{"DATABASE_URL": "postgres://user:secret@localhost:5432/db", "AUTH_COOKIE_SECURE": "false"}))
	if err != nil || insecure.AuthCookieSecure {
		t.Fatalf("secure cookie override: %+v %v", insecure, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_URL", ""}, {"DATABASE_URL", "https://user:secret@localhost/db"}, {"HTTP_ADDR", "bad"}, {"DB_MAX_CONNS", "0"}, {"READY_TIMEOUT", "0s"}, {"SHUTDOWN_TIMEOUT", "2m"}, {"AUTH_COOKIE_SECURE", "sometimes"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "postgres://user:secret@localhost/db", tc.key: tc.value}
			_, err := Load(env(values))
			if err == nil || !strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("bad error: %v", err)
			}
		})
	}
}
