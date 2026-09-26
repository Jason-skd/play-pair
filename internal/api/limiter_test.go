package api

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestLimiterBoundariesAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	l := newLimiter(2)
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow("register:ip:a", time.Hour, 5); !ok {
			t.Fatal("registration rejected early")
		}
	}
	now = now.Add(time.Minute)
	for i := 0; i < 10; i++ {
		if ok, _ := l.allow("login:email:a", 15*time.Minute, 10); !ok {
			t.Fatal("login rejected early")
		}
	}
	if ok, retry := l.allow("login:email:a", 15*time.Minute, 10); ok || retry != 15*time.Minute {
		t.Fatalf("login: %v %v", ok, retry)
	}
	now = now.Add(15 * time.Minute)
	if ok, _ := l.allow("login:email:a", 15*time.Minute, 10); !ok {
		t.Fatal("email did not expire exactly")
	}
	if ok, retry := l.allow("register:ip:a", time.Hour, 5); ok || retry != 44*time.Minute {
		t.Fatalf("long window evicted early: %v %v", ok, retry)
	}
	if ok, _ := l.allow("third", time.Minute, 1); ok {
		t.Fatal("capacity should reject")
	}
	now = now.Add(44 * time.Minute)
	if ok, _ := l.allow("third", time.Minute, 1); !ok {
		t.Fatal("expired entry did not release capacity")
	}
}

func TestLimiterConcurrentCount(t *testing.T) {
	l := newLimiter(1)
	const workers = 64
	var wg sync.WaitGroup
	results := make(chan bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ok, _ := l.allow("shared", time.Hour, 10); results <- ok }()
	}
	wg.Wait()
	close(results)
	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("allowed %d requests", allowed)
	}
}

func TestRetryAfterRoundsUp(t *testing.T) {
	s := &server{limits: newLimiter(1)}
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	s.limits.now = func() time.Time { return now }
	if ok, _ := s.limits.allow("key", time.Second+time.Nanosecond, 1); !ok {
		t.Fatal("initial request")
	}
	for _, tc := range []struct {
		elapsed time.Duration
		want    string
	}{
		{0, "2"}, {time.Nanosecond, "1"}, {time.Second, "1"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		now = time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC).Add(tc.elapsed)
		if s.allow(c, "key", time.Second+time.Nanosecond, 1) || w.Code != 429 || w.Header().Get("Retry-After") != tc.want {
			t.Fatalf("elapsed=%v status=%d retry=%q", tc.elapsed, w.Code, w.Header().Get("Retry-After"))
		}
	}
}
