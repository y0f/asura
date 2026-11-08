package storage

import (
	"strconv"
	"strings"
)

// postgresBind changes only positional parameter markers in trusted source
// SQL. Values remain driver arguments. SQL expressions and operators are
// selected explicitly by the store; they are never rewritten here.
func postgresBind(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var result strings.Builder
	result.Grow(len(query) + 16)
	parameter := 0
	for i := 0; i < len(query); {
		start := i
		switch query[i] {
		case '\'', '"', '`':
			quote := query[i]
			// Backslash escapes apply only to PostgreSQL E'...' strings.
			escaped := quote == '\'' && i > 0 && (query[i-1] == 'E' || query[i-1] == 'e') && (i == 1 || !sqlIdentifierByte(query[i-2]))
			i++
			for i < len(query) {
				if escaped && query[i] == '\\' && i+1 < len(query) {
					i += 2
					continue
				}
				if query[i] == quote {
					i++
					if i < len(query) && query[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
		case '-':
			i++
			if i < len(query) && query[i] == '-' {
				for i < len(query) && query[i] != '\n' {
					i++
				}
			}
		case '/':
			i++
			if i < len(query) && query[i] == '*' {
				i++
				for depth := 1; i < len(query) && depth > 0; {
					if i+1 < len(query) && query[i:i+2] == "/*" {
						depth++
						i += 2
					} else if i+1 < len(query) && query[i:i+2] == "*/" {
						depth--
						i += 2
					} else {
						i++
					}
				}
			}
		case '$':
			i++
			for i < len(query) && sqlIdentifierByte(query[i]) {
				i++
			}
			if i < len(query) && query[i] == '$' {
				delimiter := query[start : i+1]
				i++
				if end := strings.Index(query[i:], delimiter); end >= 0 {
					i += end + len(delimiter)
				} else {
					i = len(query)
				}
			}
		case '?':
			parameter++
			result.WriteByte('$')
			result.WriteString(strconv.Itoa(parameter))
			i++
			continue
		default:
			i++
		}
		result.WriteString(query[start:i])
	}
	return result.String()
}

func sqlIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
