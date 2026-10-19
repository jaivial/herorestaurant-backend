package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_privileged_contacts_v1
//
// The privileged group lets the bot run the whole forky catalogue, and
// admin_call replays a panel route through the REAL router. Those routes
// authenticate on the session cookie, so the bot needed a browser session it
// does not have: every admin_call died with "sesion del panel no disponible".
//
// The alternative to borrowing a human's cookie is a SERVICE SESSION: a real
// bo_sessions row owned by the bot, bound to the restaurant, with the same
// role and sections the panel would resolve. The replay then carries that
// token instead of a browser cookie, and the authorization is the real one:
// requireBOSession resolves the role, the importance and the section
// access from the database, exactly as it does for a human.
//
// The session is minted per restaurant, kept in memory, and rotated when it
// expires. It is never handed out: only the bot's own replay uses it.

// botServiceSessionTTL is how long a service session lives before it is
// re-minted. A session row that outlived the grant would keep a privileged
// door open, so it is short and it is renewed on demand.
const botServiceSessionTTL = 12 * time.Hour

// botServiceSessionErrorTTL is how long a failure to mint is remembered, so a
// database problem does not hammer the table on every privileged turn.
const botServiceSessionErrorTTL = 60 * time.Second

type botServiceSessionEntry struct {
	token   string
	err     error
	expires time.Time
	at      time.Time
}

var botServiceSessions = struct {
	sync.Mutex
	byRestaurant map[int]botServiceSessionEntry
}{byRestaurant: map[int]botServiceSessionEntry{}}

// botServiceSession returns a panel session token for the restaurant, minting
// one when there is none or the current one is expired.
//
// The user the session belongs to is the first superadmin of the deployment:
// the service session must be able to open every admin route, and there is no
// "bot user" in bo_users. The role, importance and section access are
// resolved by requireBOSession from the database, so the replay is
// authorized by the same code that authorizes a human.
func (s *Server) botServiceSession(ctx context.Context, restaurantID int) (string, error) {
	if restaurantID <= 0 || s.db == nil {
		return "", errors.New("bot service session: restaurante no disponible")
	}

	botServiceSessions.Lock()
	if e, ok := botServiceSessions.byRestaurant[restaurantID]; ok {
		if e.err != nil {
			if time.Since(e.at) < botServiceSessionErrorTTL {
				botServiceSessions.Unlock()
				return "", e.err
			}
		} else if time.Now().Before(e.expires) && e.token != "" {
			botServiceSessions.Unlock()
			return e.token, nil
		}
	}
	botServiceSessions.Unlock()

	token, err := s.mintBotServiceSession(ctx, restaurantID)
	botServiceSessions.Lock()
	entry := botServiceSessionEntry{at: time.Now()}
	if err != nil {
		entry.err = err
	} else {
		entry.token = token
		entry.expires = time.Now().Add(botServiceSessionTTL)
	}
	botServiceSessions.byRestaurant[restaurantID] = entry
	botServiceSessions.Unlock()
	return token, err
}

// mintBotServiceSession creates the bo_sessions row the replay authenticates
// on.
func (s *Server) mintBotServiceSession(ctx context.Context, restaurantID int) (string, error) {
	userID, err := s.botServiceSessionUser(ctx)
	if err != nil {
		return "", err
	}
	token, tokenSHA, err := newBOSessionToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(botServiceSessionTTL)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO bo_sessions (token_sha256, user_id, active_restaurant_id, expires_at, user_agent)
		VALUES (?, ?, ?, ?, ?)
	`, tokenSHA, userID, restaurantID, expires, "bot-privileged-session"); err != nil {
		return "", err
	}
	return token, nil
}

// botServiceSessionUser returns the user the service session belongs to: the
// first superadmin, who can open every admin route.
func (s *Server) botServiceSessionUser(ctx context.Context) (int, error) {
	var userID int
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM bo_users WHERE is_superadmin = 1 ORDER BY id LIMIT 1
	`).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("bot service session: no hay superadmin para la sesion de servicio")
		}
		return 0, err
	}
	if userID <= 0 {
		return 0, errors.New("bot service session: superadmin no valido")
	}
	return userID, nil
}

// botServiceSessionReset forgets the cached session of a restaurant, so the
// next privileged turn mints a new one. Used by the backoffice when the
// privileged contacts change, and by the tests.
func botServiceSessionReset(restaurantID int) {
	botServiceSessions.Lock()
	delete(botServiceSessions.byRestaurant, restaurantID)
	botServiceSessions.Unlock()
}

// botServiceSessionTokenForContext is the counterpart of
// boSessionTokenFromContext for the privileged path: the service session is
// put in the context so admin_call can pick it up.
func botServiceSessionTokenForContext(ctx context.Context, token string) context.Context {
	return withBOSessionToken(ctx, strings.TrimSpace(token))
}
