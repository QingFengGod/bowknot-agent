package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	// MaxChunkRunes caps one retrieval chunk.
	MaxChunkRunes = 400
	// MaxChunksPerWeek bounds the embedding cost of one week.
	MaxChunksPerWeek = 40
	// snippetRunes caps the snippet shown to the user.
	snippetRunes = 160
	// contextSnippetRunes caps one snippet injected into the prompt.
	contextSnippetRunes = 120
	// MaxContextHits is how many snippets the prompt injection may add.
	maxContextHits = 3
)

// Hit is one retrieved memory snippet.
type Hit struct {
	File    string
	Start   string
	End     string
	Snippet string
	Score   float64
}

// chunk is one retrievable paragraph of a week file.
type chunk struct {
	File  string
	Start string
	End   string
	Text  string
}

// Search retrieves memory snippets by meaning when an embedding backend is
// configured, and by keyword otherwise. Results are one per week file, best
// first.
func (s *Store) Search(ctx context.Context, query string, limit int) []Hit {
	query = strings.TrimSpace(query)
	if query == "" || s.dir == "" {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}
	chunks := s.chunks()
	if len(chunks) == 0 {
		return nil
	}
	s.mu.Lock()
	embed := s.embed
	s.mu.Unlock()
	if embed != nil {
		if hits, ok := s.semanticSearch(ctx, embed, query, chunks, limit); ok {
			return hits
		}
		log.Printf("memory: 语义检索不可用，降级为关键词检索")
	}
	hits := keywordSearch(chunks, query)
	return dedupeHits(hits, limit)
}

// Context assembles the memory section injected into the system prompt. Only
// summaries and short snippets are included, never a whole week file. The
// result is cut to budgetRunes and the cut is marked.
func (s *Store) Context(query string, budgetRunes int) string {
	if s.dir == "" {
		return ""
	}
	weeks := s.Weeks()
	if len(weeks) == 0 {
		return ""
	}
	if budgetRunes <= 0 {
		budgetRunes = 800
	}
	now := s.now()
	var b strings.Builder
	b.WriteString("## 长期记忆\n")
	b.WriteString("以下是该 Agent 按周保存的记忆摘要；引用记忆时请注明来源周。\n")
	for i := len(weeks) - 1; i >= 0; i-- {
		wk := weeks[i]
		summary := strings.TrimSpace(wk.Summary)
		switch {
		case wk.IsCurrent(now):
			summary = "（本周记录进行中，尚无摘要）"
		case wk.Status != SummaryReady || summary == "":
			summary = "（摘要待生成）"
		}
		b.WriteString("- ")
		b.WriteString(wk.Start)
		b.WriteString(" ~ ")
		b.WriteString(wk.End)
		b.WriteString("：")
		b.WriteString(oneLine(truncateRunes(summary, 200)))
		b.WriteString("\n")
	}
	if q := strings.TrimSpace(query); q != "" {
		hits := dedupeHits(keywordSearch(s.chunks(), q), maxContextHits)
		if len(hits) > 0 {
			b.WriteString("\n相关记忆片段：\n")
			for _, hit := range hits {
				b.WriteString("- 来源 ")
				b.WriteString(hit.File)
				b.WriteString("：")
				b.WriteString(oneLine(truncateRunes(hit.Snippet, contextSnippetRunes)))
				b.WriteString("\n")
			}
		}
	}
	text := b.String()
	if len([]rune(text)) <= budgetRunes {
		return text
	}
	return truncateRunes(text, budgetRunes) + "\n（记忆内容超出预算，已截断）"
}

// chunks returns every retrievable paragraph of the week files.
func (s *Store) chunks() []chunk {
	weeks := s.Weeks()
	out := make([]chunk, 0, len(weeks)*4)
	for _, wk := range weeks {
		content, err := s.ReadWeek(wk.File)
		if err != nil {
			continue
		}
		body := WeekBody(content)
		if body == "" {
			continue
		}
		chunks := chunkFile(wk, body)
		out = append(out, chunks...)
	}
	return out
}

// chunkFile splits the body of one week file into paragraphs of bounded size.
func chunkFile(wk Week, body string) []chunk {
	out := make([]chunk, 0, 4)
	for _, paragraph := range strings.Split(body, "\n\n") {
		text := strings.TrimSpace(paragraph)
		for len([]rune(text)) > MaxChunkRunes {
			runes := []rune(text)
			out = append(out, chunk{File: wk.File, Start: wk.Start, End: wk.End, Text: string(runes[:MaxChunkRunes])})
			text = strings.TrimSpace(string(runes[MaxChunkRunes:]))
		}
		if text != "" {
			out = append(out, chunk{File: wk.File, Start: wk.Start, End: wk.End, Text: text})
		}
		if len(out) >= MaxChunksPerWeek {
			break
		}
	}
	return out
}

// semanticSearch ranks the chunks by cosine similarity. It reports false when
// the embedding backend fails, so the caller falls back to keywords.
func (s *Store) semanticSearch(ctx context.Context, embed EmbedFunc, query string, chunks []chunk, limit int) ([]Hit, bool) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vectors, ok := s.embedTexts(ctx, embed, append([]string{query}, texts...))
	if !ok {
		return nil, false
	}
	queryVec := vectors[0]
	hits := make([]Hit, 0, len(chunks))
	for i, c := range chunks {
		score := cosine(queryVec, vectors[i+1])
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{
			File: c.File, Start: c.Start, End: c.End,
			Snippet: truncateRunes(c.Text, snippetRunes), Score: score,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Start > hits[j].Start
	})
	return dedupeHits(hits, limit), true
}

// embedTexts returns one vector per text, reusing the cached vectors of the
// unchanged chunks. It reports false when the backend or its reply is unusable.
func (s *Store) embedTexts(ctx context.Context, embed EmbedFunc, texts []string) ([][]float32, bool) {
	keys := make([]string, len(texts))
	for i, text := range texts {
		keys[i] = hashText(text)
	}
	vectors := make([][]float32, len(texts))
	missing := make([]string, 0, len(texts))
	missingAt := make([]int, 0, len(texts))
	s.mu.Lock()
	for i, key := range keys {
		if vec, ok := s.vectors[key]; ok {
			vectors[i] = vec
			continue
		}
		missing = append(missing, texts[i])
		missingAt = append(missingAt, i)
	}
	s.mu.Unlock()
	if len(missing) == 0 {
		return vectors, true
	}
	reply, err := embed(ctx, missing)
	if err != nil || len(reply) != len(missing) {
		return nil, false
	}
	s.mu.Lock()
	for i, vec := range reply {
		vectors[missingAt[i]] = vec
		s.vectors[keys[missingAt[i]]] = vec
	}
	s.mu.Unlock()
	return vectors, true
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// keywordSearch scores the chunks by how often the query terms appear, with a
// snippet around the strongest match.
func keywordSearch(chunks []chunk, query string) []Hit {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil
	}
	hits := make([]Hit, 0, len(chunks))
	for _, c := range chunks {
		lower := strings.ToLower(c.Text)
		score := 0
		best, bestCount := "", 0
		for _, term := range terms {
			count := strings.Count(lower, term)
			score += count
			if count > bestCount {
				best, bestCount = term, count
			}
		}
		if score == 0 {
			continue
		}
		hits = append(hits, Hit{
			File: c.File, Start: c.Start, End: c.End,
			Snippet: snippetAround(c.Text, best), Score: float64(score),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Start > hits[j].Start
	})
	return hits
}

// dedupeHits keeps the best hit of each week file.
func dedupeHits(hits []Hit, limit int) []Hit {
	if limit <= 0 {
		limit = 5
	}
	seen := map[string]bool{}
	out := make([]Hit, 0, limit)
	for _, hit := range hits {
		if seen[hit.File] {
			continue
		}
		seen[hit.File] = true
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// queryTerms splits the query into lowercase words.
func queryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// snippetAround returns a short window of text around the first occurrence of
// term, marked with ellipses at both cut ends.
func snippetAround(text, term string) string {
	if term == "" {
		return truncateRunes(text, snippetRunes)
	}
	index := strings.Index(strings.ToLower(text), term)
	if index < 0 {
		return truncateRunes(text, snippetRunes)
	}
	runes := []rune(text)
	// Map the byte index to a rune index.
	runeIndex := len([]rune(text[:index]))
	start := runeIndex - snippetRunes/3
	if start < 0 {
		start = 0
	}
	end := start + snippetRunes
	if end > len(runes) {
		end = len(runes)
	}
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}
