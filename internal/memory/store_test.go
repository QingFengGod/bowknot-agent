package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func monday(t *testing.T, layout string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(weekLayout, layout, time.Local)
	if err != nil {
		t.Fatalf("解析日期 %s: %v", layout, err)
	}
	return parsed
}

func newTestStore(t *testing.T, now string) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if now != "" {
		s.SetClock(func() time.Time { return monday(t, now).Add(12 * time.Hour) })
	}
	return s
}

func TestWeekRangeAndNaming(t *testing.T) {
	cases := []struct {
		name  string
		day   string
		file  string
		valid bool
	}{
		{"周一", "2026-10-26", "2026-10-26_2026-11-01.md", true},
		{"周三", "2026-10-28", "2026-10-26_2026-11-01.md", true},
		{"周日", "2026-11-01", "2026-10-26_2026-11-01.md", true},
		{"下周一", "2026-11-02", "2026-11-02_2026-11-08.md", true},
		{"跨年", "2027-01-01", "2026-12-28_2027-01-03.md", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			day := monday(t, tc.day)
			if got := WeekFileFor(day); got != tc.file {
				t.Fatalf("WeekFileFor(%s) = %s, 期望 %s", tc.day, got, tc.file)
			}
			start, end, ok := ParseWeekFile(tc.file)
			if !ok {
				t.Fatalf("%s 应为合法周文件", tc.file)
			}
			if WeekFileName(start) != tc.file || end.Format(weekLayout) != start.AddDate(0, 0, 6).Format(weekLayout) {
				t.Fatalf("周文件往返不一致: %s", tc.file)
			}
			if start.Weekday() != time.Monday {
				t.Fatalf("周边界不是周一: %s", start.Weekday())
			}
		})
	}
}

func TestParseWeekFileRejectsInvalidNames(t *testing.T) {
	bad := []string{
		"2026-10-26.md",
		"2026-10-26_2026-11-02.md",
		"2026-10-25_2026-10-31.md",
		"2026-10-26_2026-11-01.txt",
		"summary.json",
		"2026-10-26_2026-11-01-extra.md",
	}
	if _, _, ok := ParseWeekFile("2026-10-26_2026-11-01.md"); !ok {
		t.Fatal("合法周文件被拒绝")
	}
	for _, name := range bad {
		if _, _, ok := ParseWeekFile(name); ok {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}

func TestEnsureCreatesCurrentWeek(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	rep, err := s.Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if rep.Created != "2026-10-26_2026-11-01.md" {
		t.Fatalf("创建文件 = %s", rep.Created)
	}
	content, err := s.ReadWeek(rep.Created)
	if err != nil {
		t.Fatalf("读取当周: %v", err)
	}
	if !strings.Contains(content, "2026-10-26") || !strings.Contains(content, "2026-11-01") {
		t.Fatalf("周文件缺少标题: %q", content)
	}
	idx := readIndex(t, s)
	if idx.Schema != SchemaVersion || len(idx.Weeks) != 1 {
		t.Fatalf("索引状态异常: %+v", idx)
	}
	if idx.Weeks[0].Status != SummaryReady {
		t.Fatalf("当周状态 = %s", idx.Weeks[0].Status)
	}
	// A second check must not create anything.
	rep2, err := s.Ensure()
	if err != nil {
		t.Fatalf("第二次 Ensure: %v", err)
	}
	if rep2.Created != "" {
		t.Fatalf("重复创建: %s", rep2.Created)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tmp-") {
			t.Fatalf("残留临时文件: %s", entry.Name())
		}
	}
}

func TestEnsureMarksPastWeekPending(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	past := "2026-10-19_2026-10-25.md"
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), past), []byte(weekHeader(monday(t, "2026-10-19"))+"用户偏好浅色主题。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(rep.Pending) != 1 || rep.Pending[0] != past {
		t.Fatalf("待摘要 = %v", rep.Pending)
	}
	if rep.Created != "2026-10-26_2026-11-01.md" {
		t.Fatalf("当周未创建: %q", rep.Created)
	}
	idx := readIndex(t, s)
	for _, w := range idx.Weeks {
		switch w.File {
		case past:
			if w.Status != SummaryPending {
				t.Fatalf("上一周状态 = %s", w.Status)
			}
		case "2026-10-26_2026-11-01.md":
			if w.Status != SummaryReady {
				t.Fatalf("当周状态 = %s", w.Status)
			}
		}
	}
}

func TestSaveWeekMarksEditedPastWeekPending(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	past := "2026-10-19_2026-10-25.md"
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := s.SaveWeek(past, weekHeader(monday(t, "2026-10-19"))+"新记录。\n"); err != nil {
		t.Fatalf("SaveWeek: %v", err)
	}
	idx := readIndex(t, s)
	for _, w := range idx.Weeks {
		if w.File == past && w.Status != SummaryPending {
			t.Fatalf("编辑后的上一周状态 = %s", w.Status)
		}
	}
	if err := s.SaveWeek("bad.md", "x"); !errors.Is(err, ErrInvalidWeek) {
		t.Fatalf("非法文件名错误 = %v", err)
	}
}

func TestCorruptIndexIsRebuilt(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	past := "2026-10-19_2026-10-25.md"
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), past), []byte(weekHeader(monday(t, "2026-10-19"))+"记录。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), SummaryFileName), []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if rep.Warning == "" {
		t.Fatal("损坏回退没有警告")
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), SummaryFileName+corruptSuffix)); err != nil {
		t.Fatalf("损坏索引未保留: %v", err)
	}
	idx := readIndex(t, s)
	if idx.Schema != SchemaVersion || len(idx.Weeks) != 2 {
		t.Fatalf("重建索引异常: %+v", idx)
	}
	_ = s.Weeks()
}

func TestDeleteWeekAndClear(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	past := "2026-10-19_2026-10-25.md"
	if err := s.SaveWeek(past, "记录"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWeek(past); err != nil {
		t.Fatalf("DeleteWeek: %v", err)
	}
	if _, err := s.ReadWeek(past); !errors.Is(err, ErrWeekNotFound) {
		t.Fatalf("删除后读取 = %v", err)
	}
	if len(s.Weeks()) != 1 {
		t.Fatalf("删除后索引 = %+v", s.Weeks())
	}
	if err := s.DeleteWeek(past); !errors.Is(err, ErrWeekNotFound) {
		t.Fatalf("重复删除 = %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("清除后仍有文件: %v", entries)
	}
}

func TestExportNewestFirst(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := s.SaveWeek("2026-10-19_2026-10-25.md", "# 旧周\n\n旧周记录\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWeek("2026-10-26_2026-11-01.md", "# 本周\n\n本周记录\n"); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := s.Export(&b); err != nil {
		t.Fatalf("Export: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "Agent 记忆导出") {
		t.Fatalf("导出缺少标题: %s", out)
	}
	if strings.Index(out, "2026-10-26 ~ 2026-11-01") > strings.Index(out, "2026-10-19 ~ 2026-10-25") {
		t.Fatalf("导出未按新到旧排序: %s", out)
	}
	if strings.Contains(out, "# 本周") {
		t.Fatalf("导出不应保留标题行: %s", out)
	}
}

func TestSummarizeSuccessAndFailure(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	past := "2026-10-19_2026-10-25.md"
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), past), []byte(weekHeader(monday(t, "2026-10-19"))+"用户希望默认浅色主题。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	chat := func(ctx context.Context, system, user string) (string, error) {
		if !strings.Contains(user, "浅色主题") {
			return "", errors.New("摘要输入缺少正文")
		}
		return "```\n摘要：用户偏好浅色主题。\n```", nil
	}
	n, err := s.Summarize(context.Background(), chat)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if n != 1 {
		t.Fatalf("生成数量 = %d", n)
	}
	idx := readIndex(t, s)
	var week Week
	for _, w := range idx.Weeks {
		if w.File == past {
			week = w
		}
	}
	if week.Status != SummaryReady || week.Summary != "用户偏好浅色主题。" {
		t.Fatalf("摘要结果 = %+v", week)
	}

	// A failing model keeps the week pending and leaves the file untouched.
	chatErr := func(ctx context.Context, system, user string) (string, error) {
		return "", errors.New("网关不可用")
	}
	file := "2026-10-12_2026-10-18.md"
	if err := s.SaveWeek(file, "另一条记录"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summarize(context.Background(), chatErr); err == nil {
		t.Fatal("网关失败应返回错误")
	}
	content, err := s.ReadWeek(file)
	if err != nil || !strings.Contains(content, "另一条记录") {
		t.Fatalf("失败后周文件被改动: %q %v", content, err)
	}
	for _, w := range readIndex(t, s).Weeks {
		if w.File == file && w.Status != SummaryPending {
			t.Fatalf("失败后状态 = %s", w.Status)
		}
	}
}

func TestSummarizeSkipsEmptyWeek(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	empty := "2026-10-19_2026-10-25.md"
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), empty), []byte(weekHeader(monday(t, "2026-10-19"))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	called := false
	chat := func(ctx context.Context, system, user string) (string, error) {
		called = true
		return "x", nil
	}
	n, err := s.Summarize(context.Background(), chat)
	if err != nil || n != 0 {
		t.Fatalf("空周摘要 = %d, %v", n, err)
	}
	if called {
		t.Fatal("空周不应调用网关")
	}
	for _, w := range readIndex(t, s).Weeks {
		if w.File == empty && w.Status != SummaryReady {
			t.Fatalf("空周状态 = %s", w.Status)
		}
	}
}

func TestKeywordSearch(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	if err := s.SaveWeek("2026-10-19_2026-10-25.md", weekHeader(monday(t, "2026-10-19"))+"用户偏好浅色主题。\n\n讨论了导出功能。\n"); err != nil {
		t.Fatal(err)
	}
	hits := s.Search(context.Background(), "浅色主题", 5)
	if len(hits) == 0 {
		t.Fatal("关键词检索没有结果")
	}
	if hits[0].File != "2026-10-19_2026-10-25.md" || !strings.Contains(hits[0].Snippet, "浅色主题") {
		t.Fatalf("命中 = %+v", hits[0])
	}
	if hits := s.Search(context.Background(), "不存在的内容", 5); len(hits) != 0 {
		t.Fatalf("无关查询命中 = %+v", hits)
	}
	if hits := s.Search(context.Background(), "   ", 5); len(hits) != 0 {
		t.Fatalf("空查询命中 = %+v", hits)
	}
}

func TestSemanticSearchAndFallback(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	if err := s.SaveWeek("2026-10-19_2026-10-25.md", weekHeader(monday(t, "2026-10-19"))+"用户养了一只猫。\n\n用户提到狗的品种。\n"); err != nil {
		t.Fatal(err)
	}
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			vectors[i] = []float32{0, 1}
			if strings.Contains(text, "猫") {
				vectors[i] = []float32{1, 0}
			}
		}
		return vectors, nil
	}
	s.SetEmbed(embed)
	hits := s.Search(context.Background(), "宠物猫", 3)
	if len(hits) == 0 || !strings.Contains(hits[0].Snippet, "猫") {
		t.Fatalf("语义检索命中 = %+v", hits)
	}

	s.SetEmbed(func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, errors.New("嵌入不可用")
	})
	hits = s.Search(context.Background(), "猫", 3)
	if len(hits) == 0 {
		t.Fatal("语义不可用时未降级到关键词")
	}
	_ = s.chunks()
}

func TestContextBudgetAndSections(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	if _, err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	chat := func(ctx context.Context, system, user string) (string, error) { return "偏好浅色主题。", nil }
	past := "2026-10-19_2026-10-25.md"
	if err := s.SaveWeek(past, "用户说：偏好浅色主题。\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summarize(context.Background(), chat); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	ctx := s.Context("", 800)
	if !strings.Contains(ctx, "长期记忆") || !strings.Contains(ctx, "偏好浅色主题。") {
		t.Fatalf("上下文缺少摘要: %s", ctx)
	}
	if !strings.Contains(ctx, "本周记录进行中") {
		t.Fatalf("上下文缺少当周状态: %s", ctx)
	}
	// A pending week without summary is labelled instead of guessed.
	if err := s.SaveWeek("2026-10-12_2026-10-18.md", "未摘要记录"); err != nil {
		t.Fatal(err)
	}
	pending := s.Context("", 800)
	if !strings.Contains(pending, "摘要待生成") {
		t.Fatalf("上下文缺少待摘要标记: %s", pending)
	}
	if got := s.Context("浅色主题", 60); !strings.Contains(got, "已截断") {
		t.Fatalf("预算截断未标注: %s", got)
	}
	if got := NewStore("").Context("任意", 0); got != "" {
		t.Fatalf("空目录上下文 = %q", got)
	}
}

func TestSaveWeekRejectsOversizeAndInvalidUTF8(t *testing.T) {
	s := newTestStore(t, "2026-10-29")
	file := "2026-10-26_2026-11-01.md"
	if err := s.SaveWeek(file, string([]byte{0xff, 0xfe})); !errors.Is(err, ErrNotTextMemory) {
		t.Fatalf("非 UTF-8 错误 = %v", err)
	}
	if err := s.SaveWeek(file, strings.Repeat("a", MaxWeekFileBytes+1)); !errors.Is(err, ErrWeekTooLarge) {
		t.Fatalf("超限错误 = %v", err)
	}
}

func readIndex(t *testing.T, s *Store) Index {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir(), SummaryFileName))
	if err != nil {
		t.Fatalf("读取索引: %v", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("解析索引: %v", err)
	}
	return idx
}
