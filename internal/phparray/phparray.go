// Package phparray parses the subset of PHP needed to read Pure Blog's
// PHP data files: a file that returns an array literal, such as
// config/config.php (written by var_export) and the lang/*.php files.
package phparray

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Parse reads a PHP source file of the form `<?php ... return [ ... ];` and
// returns the array as Go values: map[string]any for associative arrays,
// []any for sequential lists, and string/int64/float64/bool/nil scalars.
func Parse(src string) (any, error) {
	p := &parser{src: src}
	if err := p.seekReturn(); err != nil {
		return nil, err
	}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return v, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) errorf(format string, args ...any) error {
	line := 1 + strings.Count(p.src[:min(p.pos, len(p.src))], "\n")
	return fmt.Errorf("phparray: line %d: %s", line, fmt.Sprintf(format, args...))
}

// seekReturn advances past the opening tag, declarations and comments up to
// and including the `return` keyword.
func (p *parser) seekReturn() error {
	for p.pos < len(p.src) {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}
		if strings.HasPrefix(p.src[p.pos:], "<?php") {
			p.pos += 5
			continue
		}
		if p.matchWord("return") {
			return nil
		}
		// Skip any other statement (e.g. declare(strict_types=1);).
		end := strings.IndexByte(p.src[p.pos:], ';')
		if end < 0 {
			return p.errorf("no return statement found")
		}
		p.pos += end + 1
	}
	return p.errorf("no return statement found")
}

func (p *parser) matchWord(word string) bool {
	if len(p.src)-p.pos < len(word) || !strings.EqualFold(p.src[p.pos:p.pos+len(word)], word) {
		return false
	}
	next := p.pos + len(word)
	if next < len(p.src) && isIdentChar(p.src[next]) {
		return false
	}
	p.pos = next
	return true
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '#' || c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/':
			end := strings.IndexByte(p.src[p.pos:], '\n')
			if end < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += end + 1
			}
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*':
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += end + 4
			}
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

// parseExpr parses a value, including `'a' . 'b'` string concatenation.
func (p *parser) parseExpr() (any, error) {
	v, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	for p.peek() == '.' {
		p.pos++
		rhs, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		v = toString(v) + toString(rhs)
	}
	return v, nil
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return ""
	}
	return ""
}

func (p *parser) parseValue() (any, error) {
	c := p.peek()
	switch {
	case c == '[':
		p.pos++
		return p.parseArray(']')
	case c == '\'':
		return p.parseSingleQuoted()
	case c == '"':
		return p.parseDoubleQuoted()
	case c == '-' || c == '+' || c >= '0' && c <= '9':
		return p.parseNumber()
	case p.matchWord("array"):
		if p.peek() != '(' {
			return nil, p.errorf("expected ( after array")
		}
		p.pos++
		return p.parseArray(')')
	case p.matchWord("true"):
		return true, nil
	case p.matchWord("false"):
		return false, nil
	case p.matchWord("null"):
		return nil, nil
	case p.matchWord("stdClass::__set_state"), p.matchWord("\\stdClass::__set_state"):
		if p.peek() != '(' {
			return nil, p.errorf("expected (")
		}
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, p.errorf("expected )")
		}
		p.pos++
		return v, nil
	}
	if c == 0 {
		return nil, p.errorf("unexpected end of input")
	}
	return nil, p.errorf("unexpected character %q", c)
}

func (p *parser) parseNumber() (any, error) {
	start := p.pos
	if p.src[p.pos] == '-' || p.src[p.pos] == '+' {
		p.pos++
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '_' || c == 'x' || c == 'X' ||
			(c == '-' || c == '+') && (p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E') {
			p.pos++
			continue
		}
		break
	}
	lit := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	if i, err := strconv.ParseInt(lit, 0, 64); err == nil {
		return i, nil
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return nil, p.errorf("invalid number %q", lit)
	}
	return f, nil
}

func (p *parser) parseSingleQuoted() (string, error) {
	p.pos++ // opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' && p.pos+1 < len(p.src) && (p.src[p.pos+1] == '\'' || p.src[p.pos+1] == '\\') {
			b.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		if c == '\'' {
			p.pos++
			return b.String(), nil
		}
		b.WriteByte(c)
		p.pos++
	}
	return "", p.errorf("unterminated string")
}

func (p *parser) parseDoubleQuoted() (string, error) {
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '"' {
			p.pos++
			return b.String(), nil
		}
		if c != '\\' || p.pos+1 >= len(p.src) {
			b.WriteByte(c)
			p.pos++
			continue
		}
		esc := p.src[p.pos+1]
		p.pos += 2
		switch esc {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'v':
			b.WriteByte('\v')
		case 'e':
			b.WriteByte(0x1b)
		case 'f':
			b.WriteByte('\f')
		case '0':
			b.WriteByte(0)
		case '\\', '"', '$':
			b.WriteByte(esc)
		case 'u':
			if p.pos < len(p.src) && p.src[p.pos] == '{' {
				end := strings.IndexByte(p.src[p.pos:], '}')
				if end > 0 {
					if cp, err := strconv.ParseUint(p.src[p.pos+1:p.pos+end], 16, 32); err == nil {
						b.WriteRune(rune(cp))
						p.pos += end + 1
						continue
					}
				}
			}
			b.WriteString("\\u")
		case 'x':
			end := p.pos
			for end < len(p.src) && end < p.pos+2 && isHex(p.src[end]) {
				end++
			}
			if end > p.pos {
				v, _ := strconv.ParseUint(p.src[p.pos:end], 16, 8)
				b.WriteByte(byte(v))
				p.pos = end
			} else {
				b.WriteString("\\x")
			}
		default:
			b.WriteByte('\\')
			b.WriteByte(esc)
		}
	}
	return "", p.errorf("unterminated string")
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

type entry struct {
	key   string
	isInt bool
	idx   int64
	val   any
}

func (p *parser) parseArray(closer byte) (any, error) {
	var entries []entry
	var nextIdx int64
	for {
		c := p.peek()
		if c == closer {
			p.pos++
			break
		}
		if c == 0 {
			return nil, p.errorf("unterminated array")
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if strings.HasPrefix(p.src[p.pos:], "=>") {
			p.pos += 2
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			e := entry{val: val}
			switch k := v.(type) {
			case int64:
				e.isInt, e.idx = true, k
				if k >= nextIdx {
					nextIdx = k + 1
				}
			case string:
				if i, err := strconv.ParseInt(k, 10, 64); err == nil && strconv.FormatInt(i, 10) == k {
					e.isInt, e.idx = true, i
					if i >= nextIdx {
						nextIdx = i + 1
					}
				} else {
					e.key = k
				}
			case bool:
				e.isInt = true
				if k {
					e.idx = 1
				}
			default:
				e.key = toString(k)
			}
			entries = append(entries, e)
		} else {
			entries = append(entries, entry{isInt: true, idx: nextIdx, val: v})
			nextIdx++
		}
		switch p.peek() {
		case ',':
			p.pos++
		case closer:
		default:
			return nil, p.errorf("expected , or %c", closer)
		}
	}
	return build(entries), nil
}

// build converts parsed entries into a []any when the keys form the
// sequence 0..n-1 (a PHP list), otherwise a map[string]any.
func build(entries []entry) any {
	isList := true
	for i, e := range entries {
		if !e.isInt || e.idx != int64(i) {
			isList = false
			break
		}
	}
	if isList {
		out := make([]any, len(entries))
		for i, e := range entries {
			out[i] = e.val
		}
		return out
	}
	out := make(map[string]any, len(entries))
	for _, e := range entries {
		k := e.key
		if e.isInt {
			k = strconv.FormatInt(e.idx, 10)
		}
		out[k] = e.val
	}
	return out
}

// Keys returns the sorted keys of a parsed map, useful for deterministic output.
func Keys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
