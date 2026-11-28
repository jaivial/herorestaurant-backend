// Command qa-http is a MANUAL QA helper for the local QA server (repair-v24):
// one request, method/URL from flags, cookie header read from a file (never
// printed), body from stdin. Prints only the status and the first -n bytes.
// Loopback URLs only.
//
//	qa-http -X PATCH -url http://127.0.0.1:18080/api/admin/stock/categories/5 -cookie-file /tmp/qa-cookie < body.json
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	method := flag.String("X", "GET", "HTTP method")
	raw := flag.String("url", "", "loopback URL")
	cookieFile := flag.String("cookie-file", "", "file with the bo_session value (or a full 'name=value')")
	headers := flag.String("H", "", "extra headers 'K: V' separated by ';' (no secrets)")
	n := flag.Int("n", 400, "max response bytes to print")
	flag.Parse()
	u, err := url.Parse(*raw)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" && u.Hostname() != "localhost") {
		fmt.Fprintln(os.Stderr, "qa-http: loopback URL required")
		os.Exit(2)
	}
	var body io.Reader
	if *method != "GET" && *method != "DELETE" {
		b, _ := io.ReadAll(os.Stdin)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(strings.ToUpper(*method), u.String(), body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qa-http: bad request")
		os.Exit(2)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range strings.Split(*headers, ";") {
		if k, v, ok := strings.Cut(h, ":"); ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	if *cookieFile != "" {
		c, err := os.ReadFile(*cookieFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "qa-http: cookie file unreadable")
			os.Exit(2)
		}
		v := strings.TrimSpace(string(c))
		if !strings.Contains(v, "=") {
			v = "bo_session=" + v
		}
		req.Header.Set("Cookie", v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qa-http: request failed")
		os.Exit(1)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, int64(*n)))
	fmt.Printf("status=%d bytes=%s%c", resp.StatusCode, b, 10)
}
