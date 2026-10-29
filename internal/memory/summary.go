package memory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

// SummarySystemPrompt asks the model for a compact week summary.
const SummarySystemPrompt = "你是 Bowknot 的记忆摘要助手。请把下面这一周的记忆整理为精简摘要，" +
	"只保留结论、稳定偏好与未完成事项，忽略闲聊与过程细节；不要编造没有记录的内容，" +
	"不超过 300 字，直接输出摘要正文。"

// Summarize generates the summaries of every pending past week. A failed call
// keeps that week pending, so the next date check retries it; the raw week file
// is never modified. It returns how many summaries were written.
func (s *Store) Summarize(ctx context.Context, chat ChatFunc) (int, error) {
	if s.dir == "" || chat == nil {
		return 0, nil
	}
	s.mu.Lock()
	if s.summarizing {
		s.mu.Unlock()
		return 0, nil
	}
	s.summarizing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.summarizing = false
		s.mu.Unlock()
	}()
	pending := s.pendingWeeks()
	generated := 0
	var errs []error
	for _, wk := range pending {
		content, err := s.ReadWeek(wk.File)
		if err != nil {
			errs = append(errs, fmt.Errorf("读取 %s: %w", wk.File, err))
			continue
		}
		body := WeekBody(content)
		if body == "" {
			// An empty week needs no model call; mark it done so that it is
			// not retried forever.
			if err := s.setSummary(wk.File, "", SummaryReady); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		user := fmt.Sprintf("周区间：%s ~ %s\n\n记忆正文：\n%s", wk.Start, wk.End,
			truncateRunes(body, maxSummaryInputRunes))
		reply, err := chat(ctx, SummarySystemPrompt, user)
		if err != nil {
			errs = append(errs, fmt.Errorf("生成 %s 摘要: %w", wk.File, err))
			continue
		}
		summary := cleanSummary(reply)
		if summary == "" {
			errs = append(errs, fmt.Errorf("生成 %s 摘要: 模型返回空内容", wk.File))
			continue
		}
		if err := s.setSummary(wk.File, summary, SummaryReady); err != nil {
			errs = append(errs, err)
			continue
		}
		generated++
	}
	if len(errs) > 0 {
		log.Printf("memory: 周摘要生成失败 %d 项: %v", len(errs), errors.Join(errs...))
		return generated, fmt.Errorf("memory: 周摘要生成失败: %w", errors.Join(errs...))
	}
	return generated, nil
}

// pendingWeeks returns the past weeks whose summary still has to be written.
func (s *Store) pendingWeeks() []Week {
	s.mu.Lock()
	defer s.mu.Unlock()
	weeks, err := s.scanWeeksLocked()
	if err != nil {
		log.Printf("memory: 读取记忆索引: %v", err)
		return nil
	}
	curKey := WeekKey(s.now())
	out := make([]Week, 0, len(weeks))
	for _, w := range weeks {
		if w.Start < curKey && w.Status != SummaryReady {
			out = append(out, w)
		}
	}
	return out
}

// cleanSummary trims a model reply to the summary limits: code fences and
// leading labels are removed, blank lines collapsed, length capped.
func cleanSummary(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 2 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "摘要：")
	text = strings.TrimPrefix(text, "摘要:")
	text = strings.TrimSpace(text)
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return truncateRunes(strings.Join(lines, "\n"), MaxSummaryRunes)
}

// truncateRunes cuts text to at most limit runes and marks the cut.
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
