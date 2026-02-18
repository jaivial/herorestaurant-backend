package api

import "context"

type boUser struct {
	ID             int      `json:"id"`
	Email          string   `json:"email"`
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	RoleImportance int      `json:"roleImportance"`
	SectionAccess  []string `json:"sectionAccess"`

	// Internal flags (not returned to clients).
	isSuperadmin bool
}

type boRestaurant struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type boSession struct {
	User               boUser         `json:"user"`
	Restaurants        []boRestaurant `json:"restaurants"`
	ActiveRestaurantID int            `json:"activeRestaurantId"`
}

type boAuth struct {
	SessionID          int64
	TokenSHA256        string
	User               boUser
	Role               string
	ActiveRestaurantID int
	MemberID           *int64
}

type boCtxKey int

const boAuthCtxKey boCtxKey = 1

func withBOAuth(ctx context.Context, a boAuth) context.Context {
	return context.WithValue(ctx, boAuthCtxKey, a)
}

func boAuthFromContext(ctx context.Context) (boAuth, bool) {
	v := ctx.Value(boAuthCtxKey)
	if v == nil {
		return boAuth{}, false
	}
	a, ok := v.(boAuth)
	return a, ok
}
