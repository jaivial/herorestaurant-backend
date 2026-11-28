package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Coordination id: google_reviews_ai_v1
//
// Thin client for the Google endpoints the review assistant needs: OAuth
// (consent URL, code exchange, refresh, revoke), Business Profile accounts and
// locations, and the v4 reviews API (list + reply). Every call returns the
// Google error message verbatim so the backoffice can show why it failed
// (e.g. the project has no Business Profile API access yet).

const (
	googleAuthURL        = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL       = "https://oauth2.googleapis.com/token"
	googleRevokeURL      = "https://oauth2.googleapis.com/revoke"
	googleAccountsURL    = "https://mybusinessaccountmanagement.googleapis.com/v1/accounts"
	googleBusinessInfo   = "https://mybusinessbusinessinformation.googleapis.com/v1/"
	googleMyBusinessV4   = "https://mybusiness.googleapis.com/v4/"
	googleBusinessScope  = "https://www.googleapis.com/auth/business.manage"
	googleReviewsTimeout = 25 * time.Second
)

var googleHTTP = &http.Client{Timeout: googleReviewsTimeout}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

type googleLocation struct {
	Account string `json:"account"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Address string `json:"address"`
}

// googleReview is the v4 review shape (only the fields we use).
type googleReview struct {
	Name     string `json:"name"`
	Reviewer struct {
		DisplayName string `json:"displayName"`
	} `json:"reviewer"`
	StarRating  string `json:"starRating"`
	Comment     string `json:"comment"`
	CreateTime  string `json:"createTime"`
	UpdateTime  string `json:"updateTime"`
	ReviewReply *struct {
		Comment string `json:"comment"`
	} `json:"reviewReply"`
}

var googleStarRatings = map[string]int{"ONE": 1, "TWO": 2, "THREE": 3, "FOUR": 4, "FIVE": 5}

func (s *Server) googleOAuthConfigured() bool {
	return s.cfg.GoogleClientID != "" && s.cfg.GoogleClientSecret != ""
}

// googleConsentURL builds the consent screen URL. access_type=offline +
// prompt=consent guarantee a refresh token on every (re)connection.
func (s *Server) googleConsentURL(redirectURI, state string) string {
	q := url.Values{
		"client_id":     {s.cfg.GoogleClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {googleBusinessScope + " openid email"},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return googleAuthURL + "?" + q.Encode()
}

// googleDo performs a request and decodes a JSON answer into out (when not
// nil). Non-2xx answers become an error carrying Google's own message.
func googleDo(ctx context.Context, method, endpoint, accessToken string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := googleHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var gErr struct {
			Error            any    `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &gErr)
		msg := strings.TrimSpace(gErr.ErrorDescription)
		if e, ok := gErr.Error.(map[string]any); ok {
			if m, _ := e["message"].(string); m != "" {
				msg = m
			}
		} else if e, ok := gErr.Error.(string); ok && msg == "" {
			msg = e
		}
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("google %d: %s", resp.StatusCode, truncateRunes(msg, 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (s *Server) googleTokenRequest(ctx context.Context, form url.Values) (googleTokenResponse, error) {
	form.Set("client_id", s.cfg.GoogleClientID)
	form.Set("client_secret", s.cfg.GoogleClientSecret)
	var out googleTokenResponse
	err := googleDo(ctx, http.MethodPost, googleTokenURL, "", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", &out)
	return out, err
}

func (s *Server) googleExchangeCode(ctx context.Context, code, redirectURI string) (googleTokenResponse, error) {
	return s.googleTokenRequest(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}})
}

func (s *Server) googleAccessToken(ctx context.Context, refreshToken string) (string, error) {
	tok, err := s.googleTokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func googleRevoke(ctx context.Context, token string) {
	_ = googleDo(ctx, http.MethodPost, googleRevokeURL+"?token="+url.QueryEscape(token), "", nil, "application/x-www-form-urlencoded", nil)
}

// googleIDTokenEmail reads the email claim of an id_token received straight
// from Google's token endpoint over TLS (no signature check needed there).
func googleIDTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(raw, &claims)
	return claims.Email
}

// googleListLocations lists every location of every account the user manages.
func googleListLocations(ctx context.Context, accessToken string) ([]googleLocation, error) {
	var accounts struct {
		Accounts []struct {
			Name string `json:"name"`
		} `json:"accounts"`
	}
	if err := googleDo(ctx, http.MethodGet, googleAccountsURL, accessToken, nil, "", &accounts); err != nil {
		return nil, err
	}
	out := []googleLocation{}
	for _, acc := range accounts.Accounts {
		var page struct {
			Locations []struct {
				Name              string `json:"name"`
				Title             string `json:"title"`
				StorefrontAddress struct {
					AddressLines []string `json:"addressLines"`
					Locality     string   `json:"locality"`
				} `json:"storefrontAddress"`
			} `json:"locations"`
		}
		endpoint := googleBusinessInfo + acc.Name + "/locations?pageSize=100&readMask=name,title,storefrontAddress"
		if err := googleDo(ctx, http.MethodGet, endpoint, accessToken, nil, "", &page); err != nil {
			return nil, err
		}
		for _, l := range page.Locations {
			addr := strings.TrimSpace(strings.Join(append(l.StorefrontAddress.AddressLines, l.StorefrontAddress.Locality), ", "))
			out = append(out, googleLocation{Account: acc.Name, Name: l.Name, Title: l.Title, Address: strings.Trim(addr, ", ")})
		}
	}
	return out, nil
}

// googleListReviews returns up to maxPages pages (50 each) of the newest
// reviews of a location ("accounts/1" + "locations/2").
func googleListReviews(ctx context.Context, accessToken, account, location string, maxPages int) ([]googleReview, error) {
	base := googleMyBusinessV4 + account + "/" + location + "/reviews?pageSize=50&orderBy=" + url.QueryEscape("updateTime desc")
	out := []googleReview{}
	token := ""
	for page := 0; page < maxPages; page++ {
		endpoint := base
		if token != "" {
			endpoint += "&pageToken=" + url.QueryEscape(token)
		}
		var res struct {
			Reviews       []googleReview `json:"reviews"`
			NextPageToken string         `json:"nextPageToken"`
		}
		if err := googleDo(ctx, http.MethodGet, endpoint, accessToken, nil, "", &res); err != nil {
			return out, err
		}
		out = append(out, res.Reviews...)
		if res.NextPageToken == "" {
			break
		}
		token = res.NextPageToken
	}
	return out, nil
}

// googlePutReply publishes (or replaces) the owner reply of one review.
func googlePutReply(ctx context.Context, accessToken, reviewName, comment string) error {
	body, _ := json.Marshal(map[string]string{"comment": comment})
	return googleDo(ctx, http.MethodPut, googleMyBusinessV4+reviewName+"/reply", accessToken, bytes.NewReader(body), "application/json", nil)
}
