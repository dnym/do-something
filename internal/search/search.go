// Package search finds approximate text matches without changing item resolution.
package search

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Match describes search relevance separately from an item's activity score.
// Relevance is an ordering value in [0,1], not a probability.
type Match struct {
	Relevance float64 `json:"relevance"`
	Field     string  `json:"field"`
	Kind      string  `json:"kind"`
	Excerpt   string  `json:"excerpt"`
}

type word struct {
	text  string
	start int
}

func words(text string) []word {
	out := []word{}
	start := -1
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			out = append(out, word{strings.ToLower(text[start:i]), start})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, word{strings.ToLower(text[start:]), start})
	}
	return out
}

// Find prefers every title match to notes matches. Within each field, exact
// phrases precede partial/reordered matches, which precede spelling matches.
func Find(query, title string, notes *string) *Match {
	if m := findField(query, title); m != nil {
		m.Field = "title"
		return m
	}
	if notes != nil {
		if m := findField(query, *notes); m != nil {
			m.Field = "notes"
			m.Relevance *= 0.5
			return m
		}
	}
	return nil
}

func findField(query, text string) *Match {
	needle := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if needle == "" {
		return nil
	}
	literal := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if literal == needle {
		return &Match{Relevance: 1, Kind: "exact", Excerpt: excerpt(text, 0)}
	}
	qw, tw := words(query), words(text)
	// Preserve literal substring behavior, including punctuation-only queries.
	if i := strings.Index(strings.ToLower(text), strings.ToLower(strings.TrimSpace(query))); i >= 0 {
		return &Match{Relevance: 0.9 + 0.09*float64(utf8.RuneCountInString(needle))/float64(max(1, utf8.RuneCountInString(literal))), Kind: "partial", Excerpt: excerpt(text, originalOffset(text, i))}
	}
	if len(qw) == 0 || len(tw) == 0 {
		return nil
	}
	qparts, tparts := make([]string, len(qw)), make([]string, len(tw))
	for i, w := range qw {
		qparts[i] = w.text
	}
	for i, w := range tw {
		tparts[i] = w.text
	}
	// Spacing and punctuation differences also allow book shelf / bookshelf.
	for _, sep := range []string{" ", ""} {
		q, t := strings.Join(qparts, sep), strings.Join(tparts, sep)
		if at := strings.Index(t, q); at >= 0 {
			pos, index := 0, 0
			for index+1 < len(tw) && pos+len(tw[index].text) <= at {
				pos += len(tw[index].text) + len(sep)
				index++
			}
			return &Match{Relevance: 0.9 + 0.09*float64(utf8.RuneCountInString(q))/float64(max(1, utf8.RuneCountInString(t))), Kind: "partial", Excerpt: excerpt(text, tw[index].start)}
		}
	}
	if len(qw) > len(tw) {
		return nil
	}
	type candidate struct {
		index       int
		quality     float64
		approximate bool
	}
	choices := make([][]candidate, len(qw))
	for i, q := range qw {
		for j, t := range tw {
			if strings.Contains(t.text, q.text) {
				choices[i] = append(choices[i], candidate{j, 1, false})
				continue
			}
			n := utf8.RuneCountInString(q.text)
			limit := 0
			if n >= 4 {
				limit = 1
			}
			if n >= 8 {
				limit = 2
			}
			if limit == 0 {
				continue
			}
			if d := distance([]rune(q.text), []rune(t.text), limit); d <= limit {
				choices[i] = append(choices[i], candidate{j, 1 - float64(d)/float64(max(n, utf8.RuneCountInString(t.text))), true})
			}
		}
		if len(choices[i]) == 0 {
			return nil
		}
		sort.SliceStable(choices[i], func(a, b int) bool { return choices[i][a].quality > choices[i][b].quality })
	}
	// Distinct query words must match distinct text words. Reassignment avoids
	// rejecting a valid multiword match when one word has several candidates.
	owner := make([]int, len(tw))
	for i := range owner {
		owner[i] = -1
	}
	assigned := make([]candidate, len(qw))
	var assign func(int, []bool, bool) bool
	assign = func(q int, seen []bool, approximate bool) bool {
		for _, c := range choices[q] {
			if c.approximate && !approximate {
				continue
			}
			if seen[c.index] {
				continue
			}
			seen[c.index] = true
			if owner[c.index] < 0 || assign(owner[c.index], seen, approximate) {
				owner[c.index] = q
				assigned[q] = c
				return true
			}
		}
		return false
	}
	order := make([]int, len(qw))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return len(choices[order[i]]) < len(choices[order[j]]) })
	for _, allowApproximate := range []bool{false, true} {
		for i := range owner {
			owner[i] = -1
		}
		complete := true
		for _, i := range order {
			if !assign(i, make([]bool, len(tw)), allowApproximate) {
				complete = false
				break
			}
		}
		if complete {
			break
		}
		if allowApproximate {
			return nil
		}
	}
	quality, first, approximate := 0.0, len(text), false
	for _, c := range assigned {
		quality += c.quality
		first = min(first, tw[c.index].start)
		approximate = approximate || c.approximate
	}
	quality /= float64(len(qw))
	if !approximate {
		return &Match{Relevance: 0.9 + 0.09*float64(len(qw))/float64(len(tw)), Kind: "partial", Excerpt: excerpt(text, first)}
	}
	return &Match{Relevance: 0.6 + 0.29*quality, Kind: "fuzzy", Excerpt: excerpt(text, first)}
}

// distance counts insertions, deletions, substitutions, and adjacent swapped
// letters. Only the narrow band within limit is evaluated; distant words exit
// early instead of constructing an unbounded quadratic matrix.
func distance(a, b []rune, limit int) int {
	if len(a)-len(b) > limit || len(b)-len(a) > limit {
		return limit + 1
	}
	cap := limit + 1
	prevprev, prev, cur := make([]int, len(b)+1), make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prevprev[j] = cap
		prev[j] = min(j, cap)
		cur[j] = cap
	}
	for i := 1; i <= len(a); i++ {
		lo, hi := max(1, i-limit), min(len(b), i+limit)
		cur[0] = min(i, cap)
		if lo > 1 {
			cur[lo-1] = cap
		}
		if hi < len(b) {
			cur[hi+1] = cap
		}
		best := cur[0]
		for j := lo; j <= hi; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prevprev[j-2]+1)
			}
			best = min(best, cur[j])
		}
		if best > limit {
			return cap
		}
		prevprev, prev, cur = prev, cur, prevprev
	}
	return prev[len(b)]
}

func excerpt(text string, at int) string {
	before := []rune(text[:at])
	start := max(0, len(before)-40)
	tail := []rune(text[at:])
	available := 160 - (len(before) - start)
	snippet := string(before[start:]) + string(tail[:min(len(tail), available)])
	snippet = strings.Join(strings.Fields(snippet), " ")
	if start > 0 {
		snippet = "…" + snippet
	}
	if len(tail) > available {
		snippet += "…"
	}
	return snippet
}

// strings.ToLower may change UTF-8 byte lengths. Convert a match position back
// to an original rune boundary before extracting the evidence snippet.
func originalOffset(text string, lowercaseIndex int) int {
	lowerPos := 0
	for i, r := range text {
		if lowerPos >= lowercaseIndex {
			return i
		}
		lowerPos += utf8.RuneLen(unicode.ToLower(r))
	}
	return len(text)
}
