package integration

import (
	"bluegrass/client-api/internal/auth"
	"bluegrass/client-api/internal/httpapi"
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/secure"
	"bluegrass/client-api/internal/session"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBurst100IndependentUsersNoPoolOverflowOrServerErrors(t *testing.T) {
	f, repo := repository(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("burst-fixture-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	args := make([]any, 0, 400)
	values := make([]string, 100)
	for i := 0; i < 100; i++ {
		values[i] = "(?,?,?,?,1)"
		args = append(args, i+1, fmt.Sprintf("burst-%03d", i), string(hash), "Fixture")
	}
	f.Exec(t, "INSERT INTO `{p}customers` (id,username,password,display_name,status) VALUES "+strings.Join(values, ","), args...)
	key := secure.Digest("burst-fixture-key")
	store, err := session.New(f.DB, session.Config{Prefix: f.Prefix, Key: key, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(repo, store, auth.Config{Key: key, AccessTTL: 30 * time.Minute, RefreshIdle: 180 * 24 * time.Hour, RetryWindow: 120 * time.Second, HashWorkers: 8, HashWait: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(svc, repo, httpapi.Config{MaxInflight: 128, RequestTimeout: 20 * time.Second, RequireHTTPS: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 25 * time.Second
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	statuses := map[int]int{}
	durations := make([]time.Duration, 0, 100)
	fail := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			begin := time.Now()
			id, e := secure.UUID()
			if e != nil {
				fail <- e
				return
			}
			payload, _ := json.Marshal(model.LoginInput{Username: fmt.Sprintf("burst-%03d", i), Password: "burst-fixture-password", DeviceID: id})
			resp, e := client.Post(server.URL+"/api/client/v1/auth/login", "application/json", bytes.NewReader(payload))
			if e != nil {
				fail <- e
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			mu.Lock()
			statuses[resp.StatusCode]++
			durations = append(durations, time.Since(begin))
			mu.Unlock()
		}(i)
	}
	begin := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(begin)
	close(fail)
	for e := range fail {
		t.Error(e)
	}
	if statuses[http.StatusOK] != 100 {
		t.Errorf("burst response counts: %v", statuses)
	}
	sourceStats, sessionStats := f.Read.Stats(), f.DB.Stats()
	if sourceStats.OpenConnections > 4 || sessionStats.OpenConnections > 4 || sourceStats.InUse != 0 || sessionStats.InUse != 0 {
		t.Error("pool budget exceeded or connections leaked")
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) == 100 {
		t.Logf("100 simultaneous users, bcrypt cost10, hash workers8, source/session pool caps4/4: success=%d wall=%s p50=%s p95=%s max=%s pool-open=%d/%d pool-in-use=%d/%d", statuses[200], elapsed.Round(time.Millisecond), durations[49].Round(time.Millisecond), durations[94].Round(time.Millisecond), durations[99].Round(time.Millisecond), sourceStats.OpenConnections, sessionStats.OpenConnections, sourceStats.InUse, sessionStats.InUse)
	}
}
