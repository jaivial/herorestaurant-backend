package api

import (
	"html"
	"net/url"
	"strings"
)

// sanitizeEmailHTML keeps a tiny allowlist of formatting tags (no attributes
// except a safe href on <a>) and escapes everything else. Used for the
// editable invoice email body. Coordination id: invoice_send_multichannel_v1
func sanitizeEmailHTML(in string) string {
	allowed := map[string]bool{"p": true, "br": true, "b": true, "strong": true, "i": true, "em": true, "u": true, "ul": true, "ol": true, "li": true, "a": true, "div": true, "h2": true, "h3": true}
	var out strings.Builder
	var stack []string
	for len(in) > 0 {
		i := strings.IndexByte(in, '<')
		if i < 0 {
			out.WriteString(html.EscapeString(html.UnescapeString(in)))
			break
		}
		out.WriteString(html.EscapeString(html.UnescapeString(in[:i])))
		in = in[i:]
		j := strings.IndexByte(in, '>')
		if j < 0 {
			break // dangling "<": drop the rest
		}
		tag := strings.TrimSpace(in[1:j])
		in = in[j+1:]
		closing := strings.HasPrefix(tag, "/")
		tag = strings.TrimPrefix(tag, "/")
		fields := strings.FieldsFunc(tag, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '/' })
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if !allowed[name] {
			continue
		}
		if closing {
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k] == name {
					for m := len(stack) - 1; m >= k; m-- {
						out.WriteString("</" + stack[m] + ">")
					}
					stack = stack[:k]
					break
				}
			}
			continue
		}
		if name == "br" {
			out.WriteString("<br>")
			continue
		}
		if name == "a" {
			href := ""
			if k := strings.Index(strings.ToLower(tag), "href="); k >= 0 {
				v := strings.TrimSpace(tag[k+5:])
				if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
					if e := strings.IndexByte(v[1:], v[0]); e >= 0 {
						href = v[1 : 1+e]
					}
				}
			}
			href = html.UnescapeString(href)
			if u, err := url.Parse(href); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
				stack = append(stack, "a")
				out.WriteString("<a href=\"" + html.EscapeString(href) + "\" style=\"color:#2563eb;\">")
			}
			continue
		}
		stack = append(stack, name)
		out.WriteString("<" + name + ">")
	}
	for k := len(stack) - 1; k >= 0; k-- {
		out.WriteString("</" + stack[k] + ">")
	}
	return out.String()
}
