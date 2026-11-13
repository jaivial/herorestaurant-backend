package dbmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Coordination id: dbmigrate_padmeta_v1 - per-collation PAD_ATTRIBUTE metadata
// and a source-only PAD-aware unique-index proposal. Nothing here executes on
// PG; the proposal is never merged into plan.sql (R5 stays as generated).

type CollationMeta struct {
	Name         string `json:"name"`
	Charset      string `json:"charset"`
	PadAttribute string `json:"pad_attribute"` // "PAD SPACE" | "NO PAD", as reported by source
}

// extractCollations records PAD_ATTRIBUTE for every collation used by a
// column or table. A failure is recorded, never guessed.
func extractCollations(ctx context.Context, q rowQ, db string, s *Schema) {
	s.CollationMetaSource = "information_schema.COLLATIONS@" + s.Version
	err := scanAll(ctx, q, `SELECT c.COLLATION_NAME, c.CHARACTER_SET_NAME, c.PAD_ATTRIBUTE FROM information_schema.COLLATIONS c
		WHERE c.COLLATION_NAME IN (SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND COLLATION_NAME IS NOT NULL
			UNION SELECT TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_COLLATION IS NOT NULL)
		ORDER BY c.COLLATION_NAME`, []any{db, db}, func(r *sql.Rows) error {
		var m CollationMeta
		if err := r.Scan(&m.Name, &m.Charset, &m.PadAttribute); err != nil {
			return err
		}
		s.Collations = append(s.Collations, m)
		return nil
	})
	if err != nil {
		s.Collations = nil
		s.CollationMetaError = err.Error()
	}
}

// PadAttribute returns the recorded attribute; ok=false when the export has no
// metadata for coll (old exports): callers must treat that as a decision gate.
func (s *Schema) PadAttribute(coll string) (string, bool) {
	for _, m := range s.Collations {
		if m.Name == coll {
			return m.PadAttribute, m.PadAttribute == "PAD SPACE" || m.PadAttribute == "NO PAD"
		}
	}
	return "", false
}

// pgCollationFor is the existing column mapping, shared by plan and proposal.
// _ci -> default collation (pgnative-A; formerly dbmigrate.ci_ai; source UCA expansions and
// general_ci vs unicode_ci vs ICU equivalence are unresolved); _bin (a text
// collation, not BINARY bytes) -> "C".
func pgCollationFor(coll string) (string, error) {
	switch {
	case strings.HasSuffix(coll, "_ci"):
		return "", nil // pgnative-A: default collation; keys via app.norm unique indexes
	case strings.HasSuffix(coll, "_bin"):
		return `"C"`, nil
	}
	return "", fmt.Errorf("unmapped collation %s", coll)
}

func padPending(s *Schema, coll string) string {
	pad, ok := s.PadAttribute(coll)
	switch {
	case !ok:
		return "pad_attribute_unknown:" + coll + " (export lacks collation metadata; explicit decision required, never default NO PAD)"
	case pad == "PAD SPACE":
		return "pad_space:" + coll + "->no_pad (" + s.CollationMetaSource + "; trailing-space equality/unique differs)"
	}
	return ""
}

type PadIndexPart struct {
	Column     string `json:"column"`
	Expression string `json:"expression"`
	Collation  string `json:"source_collation,omitempty"`
	Charset    string `json:"source_charset,omitempty"`
	Pad        string `json:"pad_attribute,omitempty"`
	Nullable   bool   `json:"nullable"`
	Generated  bool   `json:"generated"`
	Kind       string `json:"kind"`
}

type PadIndexProposal struct {
	Table  string         `json:"table"`
	Index  string         `json:"index"`
	PGName string         `json:"pg_index_name"`
	Parts  []PadIndexPart `json:"parts"`
	DDL    string         `json:"proposed_ddl,omitempty"`
	Status string         `json:"status"` // proposed_gated | blocked | not_needed
	Gates  []string       `json:"gates"`
}

// PadPlan proposes, per source unique index containing PAD SPACE text, an
// expression index keeping raw values and the column's chosen PG collation.
// Source column order, NULL handling (PG and MySQL both treat NULLs as
// distinct in unique indexes) and tenant columns are preserved as-is.
func PadPlan(s *Schema) []PadIndexProposal {
	var out []PadIndexProposal
	for _, t := range s.Tables {
		cols := map[string]Column{}
		for _, c := range t.Columns {
			cols[c.Name] = c
		}
		for _, ix := range t.Indexes {
			if !ix.Unique {
				continue
			}
			p := PadIndexProposal{Table: t.Name, Index: ix.Name, PGName: PGName("ixpad", t.Name, ix.Name), Status: "not_needed"}
			var exprs []string
			padded, blocked := false, false
			for _, ic := range ix.Cols {
				c, ok := cols[ic.Name]
				if !ok || ic.SubPart != nil || ic.Expr != nil {
					blocked = true
					p.Gates = append(p.Gates, "prefix_or_functional_part:"+ic.Name)
					continue
				}
				k, _ := ColumnKind(c)
				part := PadIndexPart{Column: c.Name, Expression: pq(c.Name), Nullable: c.Nullable, Generated: c.GenExpr != "", Kind: c.DataType}
				if c.Collation != nil {
					part.Collation = *c.Collation
					for _, m := range s.Collations {
						if m.Name == *c.Collation {
							part.Charset = m.Charset
						}
					}
				}
				if c.GenExpr != "" {
					p.Gates = append(p.Gates, "generated_column_pg_expression_review:"+c.Name)
				}
				switch {
				case k == KEnum:
					p.Gates = append(p.Gates, "enum_input_trailing_space_parity_gated:"+c.Name)
				case (k == KText || k == KChar) && c.Collation != nil:
					pad, known := s.PadAttribute(*c.Collation)
					part.Pad = pad
					target, err := pgCollationFor(*c.Collation)
					if err != nil {
						blocked = true
						p.Gates = append(p.Gates, err.Error())
						break
					}
					if !known {
						blocked = true
						p.Gates = append(p.Gates, "pad_attribute_unknown:"+*c.Collation)
						break
					}
					if pad == "PAD SPACE" {
						padded = true
						// index_elem form, identical to the runtime ON CONFLICT target.
						part.Expression = "(rtrim(" + pq(c.Name) + ", ' ')) COLLATE " + quoteColl(target)
						if target == "dbmigrate.ci_ai" {
							p.Gates = append(p.Gates, "ci_ai_approximates_"+*c.Collation+"_expansions_unresolved:"+c.Name)
						}
						if c.DataType == "char" {
							p.Gates = append(p.Gates, "char_bpchar_semantics_review:"+c.Name)
						}
					}
				}
				if ic.Desc {
					part.Expression += " DESC"
				}
				p.Parts = append(p.Parts, part)
				exprs = append(exprs, part.Expression)
			}
			switch {
			case blocked:
				p.Status = "blocked"
			case padded:
				p.Status = "proposed_gated"
				p.DDL = "CREATE UNIQUE INDEX " + pq(p.PGName) + " ON " + pq(t.Name) + " (" + strings.Join(exprs, ", ") + ")"
				p.Gates = append(p.Gates, "caller_compat_pending: every ON CONFLICT target, unique-error name and equality reader must use the identical expression (docs/dbmigrate/padding-index-callers.tsv)",
					"replaces_r5_raw_unique_only_after_review: not merged into plan.sql")
			}
			if p.Status == "not_needed" && len(p.Gates) > 0 {
				p.Status = "gated_no_pad_expression"
			}
			if p.Status != "not_needed" {
				out = append(out, p)
			}
		}
	}
	return out
}

func quoteColl(c string) string {
	if strings.HasPrefix(c, `"`) {
		return c
	}
	parts := strings.SplitN(c, ".", 2)
	if len(parts) == 2 {
		return pq(parts[0]) + "." + pq(parts[1])
	}
	return pq(c)
}
