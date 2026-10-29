// Package memory 实现每个 Agent 的按周长期记忆：周文件、summary.json 索引、
// 对话前的日期触发、跨周摘要生成、检索与上下文注入。
package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 记忆文件的结构与上限。schema 变更时必须递增并处理旧版本。
const (
	// SummaryFileName is the index file inside the memories directory.
	SummaryFileName = "summary.json"
	// SchemaVersion is the index schema this build reads.
	SchemaVersion = 1
	// WeekFileExt is the extension of a weekly memory file.
	WeekFileExt = ".md"
	// MaxWeekFileBytes caps one weekly memory file.
	MaxWeekFileBytes = 1 << 20
	// MaxIndexBytes caps the index file.
	MaxIndexBytes = 64 << 10
	// MaxSummaryRunes caps a generated week summary.
	MaxSummaryRunes = 400
	// maxSummaryInputRunes caps the week text sent to the model.
	maxSummaryInputRunes = 6000
	// weekLayout is the date layout of a week boundary.
	weekLayout = "2006-01-02"
	// corruptSuffix marks an index preserved after a decode failure.
	corruptSuffix = ".corrupt"
)

// SummaryStatus is the summary state of one week.
type SummaryStatus string

// Summary states. A pending week is summarized again on the next date check.
const (
	SummaryReady   SummaryStatus = "ready"
	SummaryPending SummaryStatus = "pending"
)

// Errors reported to the caller.
var (
	ErrInvalidWeek   = errors.New("记忆周文件名非法")
	ErrWeekNotFound  = errors.New("记忆周文件不存在")
	ErrWeekTooLarge  = errors.New("记忆周文件超过上限")
	ErrNotTextMemory = errors.New("记忆必须是 UTF-8 文本")
)

// Week is one weekly memory entry of the index.
type Week struct {
	Start     string        `json:"start"`
	End       string        `json:"end"`
	File      string        `json:"file"`
	Summary   string        `json:"summary,omitempty"`
	Status    SummaryStatus `json:"summary_status"`
	UpdatedAt int64         `json:"updated_at"`
}

// Index is the content of summary.json: the week list with its summaries.
type Index struct {
	Schema    int    `json:"schema"`
	UpdatedAt int64  `json:"updated_at"`
	Weeks     []Week `json:"weeks"`
}

// Report is the outcome of one date check.
type Report struct {
	// Created is the week file created for the current week, if any.
	Created string
	// Pending lists the past week files whose summary is missing.
	Pending []string
	// Warning explains a recovered or skipped file, or is empty.
	Warning string
}

// ChatFunc completes one summary prompt. A nil function disables summary
// generation and leaves the week pending for a later retry.
type ChatFunc func(ctx context.Context, system, user string) (string, error)

// EmbedFunc returns one vector per text. A nil function keeps retrieval on the
// keyword path.
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)

// WeekRange returns the Monday and Sunday of the week containing t.
func WeekRange(t time.Time) (time.Time, time.Time) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	offset := (int(day.Weekday()) + 6) % 7
	start := day.AddDate(0, 0, -offset)
	return start, start.AddDate(0, 0, 6)
}

// WeekFileName returns the file name of the week starting at start.
func WeekFileName(start time.Time) string {
	return start.Format(weekLayout) + "_" + start.AddDate(0, 0, 6).Format(weekLayout) + WeekFileExt
}

// WeekFileFor returns the week file name that contains t.
func WeekFileFor(t time.Time) string {
	start, _ := WeekRange(t)
	return WeekFileName(start)
}

// WeekKey returns the sortable date of a week boundary.
func WeekKey(t time.Time) string {
	start, _ := WeekRange(t)
	return start.Format(weekLayout)
}

// ParseWeekFile validates a week file name and returns its boundaries.
func ParseWeekFile(name string) (start, end time.Time, ok bool) {
	if !strings.HasSuffix(name, WeekFileExt) {
		return time.Time{}, time.Time{}, false
	}
	body := strings.TrimSuffix(name, WeekFileExt)
	parts := strings.Split(body, "_")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, false
	}
	var err error
	start, err = time.Parse(weekLayout, parts[0])
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	end, err = time.Parse(weekLayout, parts[1])
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if start.Weekday() != time.Monday || !end.Equal(start.AddDate(0, 0, 6)) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// ValidWeekFile reports whether name is a week file name.
func ValidWeekFile(name string) bool {
	_, _, ok := ParseWeekFile(name)
	return ok
}

// IsCurrent reports whether the week of w contains t.
func (w Week) IsCurrent(t time.Time) bool {
	start, _ := WeekRange(t)
	return w.Start == start.Format(weekLayout)
}

// WeekBody returns the user text of one week file: heading lines and blank
// lines are dropped, so an untouched file counts as empty.
func WeekBody(content string) string {
	lines := make([]string, 0, 16)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
}

func weekHeader(start time.Time) string {
	return fmt.Sprintf("# 记忆 %s ~ %s\n\n", start.Format(weekLayout), start.AddDate(0, 0, 6).Format(weekLayout))
}
