// Package promtext parses individual Prometheus text exposition samples.
package promtext

import (
	"math"
	"strconv"
	"strings"
)

// Sample is one parsed exposition line. Labels holds the raw label block,
// including braces, so callers can match labels without a full label parser.
type Sample struct {
	Name   string
	Labels string
	Value  float64
}

// ParseLine parses `name{labels} value [timestamp]`. Label values may contain
// spaces, braces, and escaped quotes. Comments, blank lines, malformed lines,
// and non-finite values return ok=false.
func ParseLine(line string) (Sample, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Sample{}, false
	}
	nameEnd := strings.IndexAny(line, "{ \t")
	if nameEnd <= 0 {
		return Sample{}, false
	}
	s := Sample{Name: line[:nameEnd]}
	rest := line[nameEnd:]
	if rest[0] == '{' {
		end := labelBlockEnd(rest)
		if end < 0 {
			return Sample{}, false
		}
		s.Labels = rest[:end+1]
		rest = rest[end+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) < 1 || len(fields) > 2 {
		return Sample{}, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return Sample{}, false
	}
	s.Value = v
	return s, true
}

// Label returns the unescaped value of a label in the sample's label block.
func (s Sample) Label(name string) (string, bool) {
	block := strings.TrimSuffix(strings.TrimPrefix(s.Labels, "{"), "}")
	for block != "" {
		block = strings.TrimLeft(block, " \t,")
		eq := strings.IndexByte(block, '=')
		if eq < 0 || eq+1 >= len(block) || block[eq+1] != '"' {
			return "", false
		}
		key := strings.TrimSpace(block[:eq])
		var value strings.Builder
		i := eq + 2
		for ; i < len(block); i++ {
			c := block[i]
			if c == '\\' && i+1 < len(block) {
				i++
				switch block[i] {
				case 'n':
					value.WriteByte('\n')
				default:
					value.WriteByte(block[i])
				}
				continue
			}
			if c == '"' {
				break
			}
			value.WriteByte(c)
		}
		if i >= len(block) {
			return "", false
		}
		if key == name {
			return value.String(), true
		}
		block = block[i+1:]
	}
	return "", false
}

// labelBlockEnd returns the index of the closing brace, skipping quoted values.
func labelBlockEnd(s string) int {
	quoted := false
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == '}':
			return i
		}
	}
	return -1
}
