package main

// Coordination id: pg_local_http_qa_v1 (repair-v19). Local MANUAL HTTP QA
// mode for the PostgreSQL port. It is NOT a cutover: normal runs keep the
// DB_DRIVER=postgres refusal of main.go. This path is taken only when
// VC_PG_LOCAL_HTTP_QA=1 AND every guard below passes; any failure is fatal.
// Intended to run inside an isolated network namespace (no network) under the
// cgroup limits, against the scratch PG only (see docs/result-repair-v19.md).

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"preactvillacarmen/internal/config"
)

const pgLocalQAEnv = "VC_PG_LOCAL_HTTP_QA"

// providerEnvRE matches configuration that could reach a real provider. In
// QA mode every such variable must be unset or empty (never printed).
var providerEnvRE = regexp.MustCompile("(?i)(STRIPE|UAZAPI|WHATSAPP|EVOLUTION|WA_|SMTP|RESEND|MAILGUN|SENDGRID|POSTMARK|TWILIO|OPENAI|ANTHROPIC|DEEPSEEK|MINIMAX|GEMINI|MISTRAL|OPENROUTER|CLOUDFLARE|CF_|BUNNY|NAMECHEAP|INSTATIC|TELEGRAM|SLACK|WEBHOOK|S3_|AWS_|R2_|TITAN)")

// localQAGuards refuses unless the process is confined to a local, inert
// setup. It never echoes values (names only).
func localQAGuards(cfg config.Config) error {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return errors.New("listen address must be loopback (set ADDR/PORT so the host is 127.0.0.1, ::1 or localhost)")
	}
	if v := strings.TrimSpace(os.Getenv("BO_DISABLE_BACKGROUND_WORKERS")); v == "" || v == "0" {
		return errors.New("BO_DISABLE_BACKGROUND_WORKERS must be set (no reminder/outbox/watchdog/bounce/stock loops)")
	}
	if !loopbackDSN(cfg.DatabaseURL) {
		return errors.New("DATABASE_URL must point to a loopback host or a unix socket (scratch PG only)")
	}
	var bad []string
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if strings.TrimSpace(val) != "" && providerEnvRE.MatchString(name) {
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("provider configuration present (must be unset in QA mode): %s", strings.Join(bad, ","))
	}
	return nil
}

func loopbackDSN(dsn string) bool {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return false
	}
	if h := u.Query().Get("host"); h != "" {
		return strings.HasPrefix(h, "/")
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// refuseTransport fails every outbound request made through the default
// HTTP client/transport. Clients with their own Transport are NOT covered;
// the network namespace is the primary isolation.
type refuseTransport struct{}

func (refuseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("outbound HTTP disabled in %s mode (%s)", pgLocalQAEnv, r.URL.Host)
}

func disableOutboundHTTP() {
	http.DefaultTransport = refuseTransport{}
	http.DefaultClient.Transport = refuseTransport{}
}

const pgLocalQAWaiveEnv = "VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE"

// localQAWaivers reads the scratch-only waiver list (e.g.
// "import-marker,shadow-fk"). Empty: no waiver (default). It is honoured only
// in VC_PG_LOCAL_HTTP_QA mode (this function is only called there) and only
// when DATABASE_URL is a unix socket or loopback (re-checked here). Every
// waiver is announced with a banner; unknown names are refused downstream.
func localQAWaivers(cfg config.Config) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(pgLocalQAWaiveEnv))
	if raw == "" {
		return nil, nil
	}
	if !loopbackDSN(cfg.DatabaseURL) {
		return nil, fmt.Errorf("%s requires DATABASE_URL on a unix socket or loopback (never TCP non-loopback)", pgLocalQAWaiveEnv)
	}
	ws := strings.Split(raw, ",")
	for _, w := range ws {
		log.Printf("[%s] *** WAIVED on disposable scratch: %s *** (QA only; orphans untouched; not valid for any other database)", pgLocalQAWaiveEnv, strings.TrimSpace(w))
	}
	return ws, nil
}
