package api

import "time"

// LegalPageSummary is a lightweight row for the admin list view.
type LegalPageSummary struct {
	Slug          string    `json:"slug"`
	Title         string    `json:"title"`
	UpdatedAt     time.Time `json:"updatedAt"`
	UpdatedByName string    `json:"updatedByName,omitempty"`
}

// LegalPage is the full row (editor restore / public render).
type LegalPage struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	ContentJSON string    `json:"contentJson"`
	ContentHTML string    `json:"contentHtml"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// LegalPageListResponse is the admin list endpoint payload.
type LegalPageListResponse struct {
	Success bool               `json:"success"`
	Pages   []LegalPageSummary `json:"pages"`
}

// LegalPageUpsertRequest is the admin POST body.
type LegalPageUpsertRequest struct {
	Title       string `json:"title"`
	ContentJSON string `json:"contentJson"`
	ContentHTML string `json:"contentHtml"`
}
