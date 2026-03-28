package api

import "testing"

func TestBuildBookingGeneralSearchWhere(t *testing.T) {
	tests := []struct {
		name          string
		searchName    string
		searchPhone   string
		wantEmpty     bool
		wantNameLike  bool
		wantPhoneLike bool
	}{
		{
			name:        "empty params produce empty clause",
			searchName:  "",
			searchPhone: "",
			wantEmpty:   true,
		},
		{
			name:         "name only produces name LIKE clause",
			searchName:   "Beatriz",
			searchPhone:  "",
			wantNameLike: true,
		},
		{
			name:          "phone only produces phone LIKE clause",
			searchName:    "",
			searchPhone:   "67999",
			wantPhoneLike: true,
		},
		{
			name:          "both params produce combined clause",
			searchName:    "Gil",
			searchPhone:   "679",
			wantNameLike:  true,
			wantPhoneLike: true,
		},
		{
			name:        "whitespace-only params produce empty clause",
			searchName:  "   ",
			searchPhone: "  ",
			wantEmpty:   true,
		},
		{
			name:          "phone strips non-digits",
			searchName:    "",
			searchPhone:   "+34 679-992",
			wantPhoneLike: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clause, args := buildBookingGeneralSearchWhere(tt.searchName, tt.searchPhone)

			if tt.wantEmpty {
				if clause != "" {
					t.Fatalf("expected empty clause, got %q", clause)
				}
				if len(args) != 0 {
					t.Fatalf("expected no args, got %v", args)
				}
				return
			}

			if clause == "" {
				t.Fatal("expected non-empty clause, got empty")
			}

			if tt.wantNameLike && len(args) < 2 {
				t.Fatal("expected at least 2 args for name LIKE (customer_name + contact_email)")
			}

			if tt.wantPhoneLike {
				hasPhoneArg := false
				for _, a := range args {
					s, ok := a.(string)
					if ok && len(s) > 0 {
						for _, c := range s {
							if c >= '0' && c <= '9' {
								hasPhoneArg = true
								break
							}
						}
					}
				}
				if !hasPhoneArg {
					t.Fatal("expected phone-related arg with digits")
				}
			}
		})
	}
}

func TestBuildBookingGeneralSearchWhereNameArgs(t *testing.T) {
	clause, args := buildBookingGeneralSearchWhere("Beatriz", "")

	if len(args) != 2 {
		t.Fatalf("expected 2 args (name + email LIKE), got %d", len(args))
	}

	namePattern, ok := args[0].(string)
	if !ok {
		t.Fatalf("expected string arg, got %T", args[0])
	}
	if namePattern != "%Beatriz%" {
		t.Fatalf("expected %%Beatriz%%, got %q", namePattern)
	}

	emailPattern, ok := args[1].(string)
	if !ok {
		t.Fatalf("expected string arg, got %T", args[1])
	}
	if emailPattern != "%Beatriz%" {
		t.Fatalf("expected %%Beatriz%%, got %q", emailPattern)
	}

	_ = clause
}

func TestBuildBookingGeneralSearchWherePhoneDigitsOnly(t *testing.T) {
	_, args := buildBookingGeneralSearchWhere("", "+34 679-992-249")

	if len(args) != 1 {
		t.Fatalf("expected 1 arg (phone LIKE), got %d", len(args))
	}

	phonePattern, ok := args[0].(string)
	if !ok {
		t.Fatalf("expected string arg, got %T", args[0])
	}

	if phonePattern != "34679992249%" {
		t.Fatalf("expected 34679992249%%, got %q", phonePattern)
	}
}

func TestBuildBookingGeneralSearchWhereCombinedArgs(t *testing.T) {
	_, args := buildBookingGeneralSearchWhere("Gil", "679")

	if len(args) != 3 {
		t.Fatalf("expected 3 args (name, email, phone), got %d", len(args))
	}

	namePattern := args[0].(string)
	emailPattern := args[1].(string)
	phonePattern := args[2].(string)

	if namePattern != "%Gil%" {
		t.Fatalf("name: expected %%Gil%%, got %q", namePattern)
	}
	if emailPattern != "%Gil%" {
		t.Fatalf("email: expected %%Gil%%, got %q", emailPattern)
	}
	if phonePattern != "679%" {
		t.Fatalf("phone: expected 679%%, got %q", phonePattern)
	}
}

func TestGeneralSearchCountDefault(t *testing.T) {
	got := generalSearchSanitizeCount(0)
	if got != 15 {
		t.Fatalf("expected default 15, got %d", got)
	}
}

func TestGeneralSearchCountClampMax(t *testing.T) {
	got := generalSearchSanitizeCount(200)
	if got != 100 {
		t.Fatalf("expected clamped to 100, got %d", got)
	}
}

func TestGeneralSearchCountValid(t *testing.T) {
	got := generalSearchSanitizeCount(25)
	if got != 25 {
		t.Fatalf("expected 25, got %d", got)
	}
}

func TestGeneralSearchCountMinOne(t *testing.T) {
	got := generalSearchSanitizeCount(-5)
	if got != 15 {
		t.Fatalf("expected default 15 for negative, got %d", got)
	}
}

func TestGeneralSearchPageDefault(t *testing.T) {
	got := generalSearchSanitizePage(0)
	if got != 1 {
		t.Fatalf("expected default 1, got %d", got)
	}
}

func TestGeneralSearchPageValid(t *testing.T) {
	got := generalSearchSanitizePage(3)
	if got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}

func TestGeneralSearchPhoneDigitsOnlyEmpty(t *testing.T) {
	got := bookingSearchDigitsOnly("abc-def")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
