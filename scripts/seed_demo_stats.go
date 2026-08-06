//go:build ignore

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

func main() {
	dsn := "file:/tmp/edp-web2.db?_pragma=busy_timeout(5000)"
	st, err := store.Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		panic(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	var events []record.Event
	repos := []struct{ reg, repo string }{
		{"dockerhub", "library/nginx"},
		{"dockerhub", "library/redis"},
		{"dockerhub", "library/postgres"},
		{"ghcr", "actions/runner"},
		{"ghcr", "cli/cli"},
	}
	ips := []string{"10.0.1.12", "10.0.1.34", "192.168.8.20", "203.0.113.9"}
	for d := 6; d >= 0; d-- {
		day := now.AddDate(0, 0, -d)
		for i, r := range repos {
			n := 3 + (i+d)%5
			for j := 0; j < n; j++ {
				events = append(events, record.Event{
					TS: day.Add(time.Duration(j+i) * time.Hour), ClientIP: ips[(i+j)%len(ips)],
					Registry: r.reg, Host: r.reg + ".example.com",
					EventType: record.EventManifest, Repository: r.repo, Reference: "latest",
					Method: "GET", Status: 200, Bytes: int64(800 + i*100), DurationMS: 20 + int64(j),
				})
				events = append(events, record.Event{
					TS: day.Add(time.Duration(j+i)*time.Hour + time.Minute), ClientIP: ips[(i+j)%len(ips)],
					Registry: r.reg, Host: r.reg + ".example.com",
					EventType: record.EventBlob, Repository: r.repo, Reference: "sha256:demo",
					Method: "GET", Status: 200, Bytes: int64(1_200_000*(i+1) + d*10000), DurationMS: 80,
				})
				if j == 0 {
					events = append(events, record.Event{
						TS: day.Add(2 * time.Hour), ClientIP: ips[i%len(ips)],
						Registry: r.reg, Host: r.reg + ".example.com",
						EventType: record.EventManifest, Repository: r.repo, Reference: "v1." + string(rune('0'+d%10)),
						Method: "GET", Status: 200, Bytes: 500, DurationMS: 15,
					})
				}
			}
			if d%2 == 0 && i%2 == 0 {
				events = append(events, record.Event{
					TS: day.Add(3 * time.Hour), ClientIP: ips[i%len(ips)],
					Registry: r.reg, Host: r.reg + ".example.com",
					EventType: record.EventManifest, Repository: r.repo, Reference: "missing",
					Method: "GET", Status: 404, Bytes: 0, DurationMS: 5, Error: "not found",
				})
			}
		}
	}
	if err := st.WriteBatch(context.Background(), events); err != nil {
		panic(err)
	}
	fmt.Printf("seeded %d events\n", len(events))
}
