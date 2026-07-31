package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "", "backoffice origin")
	cookie := flag.String("cookie", "", "bo_session cookie value")
	ticket := flag.Int64("ticket", 0, "open ticket id")
	version := flag.Int("version", 1, "ticket version")
	amount := flag.Int64("amount", 0, "ticket cents")
	workers := flag.Int("workers", 8, "concurrent retries")
	flag.Parse()
	if *base == "" || *cookie == "" || *ticket <= 0 || *amount <= 0 {
		flag.Usage()
		os.Exit(2)
	}
	command := fmt.Sprintf("load-%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]any{"idempotencyKey": command, "expectedVersion": *version, "payments": []map[string]any{{"method": "CASH", "amountCents": *amount, "idempotencyKey": command + "-payment"}}, "closeVisit": true})
	var ok, failed int64
	var wg sync.WaitGroup
	start := time.Now()
	for range *workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/admin/pos/tickets/%d/checkout", *base, *ticket), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", "bo_session="+*cookie)
			res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				atomic.AddInt64(&failed, 1)
				fmt.Fprintln(os.Stderr, err)
				return
			}
			defer res.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			if res.StatusCode == http.StatusOK {
				atomic.AddInt64(&ok, 1)
			} else {
				atomic.AddInt64(&failed, 1)
				fmt.Fprintf(os.Stderr, "status=%d body=%s\n", res.StatusCode, raw)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("workers=%d ok=%d failed=%d duration=%s command=%s\n", *workers, ok, failed, time.Since(start), command)
	if failed > 0 {
		os.Exit(1)
	}
}
