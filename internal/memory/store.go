package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	memDirMode  = 0o700
	memFileMode = 0o600
)

// Store keeps the weekly memories of one agent under its memories directory.
// An empty directory keeps the store inert, which the in-memory workspaces and
// the tests use. Every method is safe to call from any goroutine.
type Store struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	embed   EmbedFunc
	vectors map[string][]float32

	// summarizing keeps one background summary run per agent; a date check
	// during that run does not start a second one.
	summarizing bool
}

// NewStore returns a store over one agent memories directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir, now: time.Now, vectors: map[string][]float32{}}
}

// Dir returns the memories directory.
func (s *Store) Dir() string { return s.dir }

// SetClock replaces the clock, which the tests use for week boundaries.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

// SetEmbed installs the optional semantic retrieval backend. A nil function
// keeps retrieval on the keyword path.
func (s *Store) SetEmbed(fn EmbedFunc) {
	s.mu.Lock()
	s.embed = fn
	s.mu.Unlock()
}

// Ensure runs the date check: it creates the current week file, rebuilds the
// index when needed and marks every past week without a summary as pending.
func (s *Store) Ensure() (Report, error) {
	if s.dir == "" {
		return Report{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureLocked()
}

func (s *Store) ensureLocked() (Report, error) {
	if err := os.MkdirAll(s.dir, memDirMode); err != nil {
		return Report{}, fmt.Errorf("memory: 创建记忆目录: %w", err)
	}
	rep := Report{}
	idx, err := s.loadIndexLocked(&rep)
	if err != nil {
		return rep, err
	}
	files, err := s.listWeekFilesLocked()
	if err != nil {
		return rep, err
	}
	now := s.now()
	curStart, _ := WeekRange(now)
	curKey := curStart.Format(weekLayout)
	curFile := WeekFileName(curStart)
	changed := false

	weeks := make(map[string]Week, len(idx.Weeks))
	for _, w := range idx.Weeks {
		weeks[w.File] = w
	}
	// An entry whose file was deleted by hand is dropped from the index.
	for name := range weeks {
		if _, ok := files[name]; !ok {
			delete(weeks, name)
			changed = true
		}
	}
	for name, info := range files {
		w, ok := weeks[name]
		if !ok {
			start, end, _ := ParseWeekFile(name)
			w = Week{
				Start:     start.Format(weekLayout),
				End:       end.Format(weekLayout),
				File:      name,
				Status:    SummaryReady,
				UpdatedAt: info.ModTime().Unix(),
			}
			if w.Start < curKey {
				w.Status = SummaryPending
			}
			changed = true
		}
		// A file edited after its summary needs a new one.
		if w.Start < curKey && info.ModTime().Unix() > w.UpdatedAt && w.Status == SummaryReady {
			w.Status = SummaryPending
			changed = true
		}
		weeks[name] = w
	}
	if _, ok := files[curFile]; !ok {
		if err := writeFile(filepath.Join(s.dir, curFile), []byte(weekHeader(curStart)), memFileMode); err != nil {
			return rep, fmt.Errorf("memory: 创建周文件: %w", err)
		}
		weeks[curFile] = Week{
			Start:     curKey,
			End:       curStart.AddDate(0, 0, 6).Format(weekLayout),
			File:      curFile,
			Status:    SummaryReady,
			UpdatedAt: now.Unix(),
		}
		rep.Created = curFile
		changed = true
	}
	idx.Weeks = sortedWeeks(weeks)
	for _, w := range idx.Weeks {
		if w.Start < curKey && w.Status != SummaryReady {
			rep.Pending = append(rep.Pending, w.File)
		}
	}
	if changed {
		idx.Schema = SchemaVersion
		idx.UpdatedAt = now.Unix()
		if err := s.saveIndexLocked(idx); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// Weeks returns the weekly entries of the index, oldest first. Missing index
// entries are added from the files on disk without writing anything.
func (s *Store) Weeks() []Week {
	if s.dir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	weeks, err := s.scanWeeksLocked()
	if err != nil {
		log.Printf("memory: 读取记忆索引: %v", err)
		return nil
	}
	return weeks
}

// ReadWeek returns the text of one week file.
func (s *Store) ReadWeek(file string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readWeekLocked(file)
}

func (s *Store) readWeekLocked(file string) (string, error) {
	if !ValidWeekFile(file) {
		return "", ErrInvalidWeek
	}
	info, err := os.Stat(filepath.Join(s.dir, file))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrWeekNotFound
	}
	if err != nil {
		return "", fmt.Errorf("memory: 读取周文件: %w", err)
	}
	if info.Size() > MaxWeekFileBytes {
		return "", ErrWeekTooLarge
	}
	data, err := os.ReadFile(filepath.Join(s.dir, file))
	if err != nil {
		return "", fmt.Errorf("memory: 读取周文件: %w", err)
	}
	if !utf8.Valid(data) {
		return "", ErrNotTextMemory
	}
	return string(data), nil
}

// SaveWeek replaces one week file atomically. An edited past week is marked
// pending so that its summary is regenerated on the next date check.
func (s *Store) SaveWeek(file, content string) error {
	if s.dir == "" {
		return nil
	}
	if !ValidWeekFile(file) {
		return ErrInvalidWeek
	}
	if !utf8.ValidString(content) {
		return ErrNotTextMemory
	}
	if len(content) > MaxWeekFileBytes {
		return ErrWeekTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, memDirMode); err != nil {
		return fmt.Errorf("memory: 创建记忆目录: %w", err)
	}
	if err := writeFile(filepath.Join(s.dir, file), []byte(content), memFileMode); err != nil {
		return fmt.Errorf("memory: 保存周文件: %w", err)
	}
	idx, err := s.loadIndexLocked(&Report{})
	if err != nil {
		return err
	}
	now := s.now()
	curKey := WeekKey(now)
	start, end, _ := ParseWeekFile(file)
	w := Week{
		Start:     start.Format(weekLayout),
		End:       end.Format(weekLayout),
		File:      file,
		Status:    SummaryReady,
		UpdatedAt: now.Unix(),
	}
	if w.Start < curKey {
		w.Status = SummaryPending
	}
	replaced := false
	for i := range idx.Weeks {
		if idx.Weeks[i].File != file {
			continue
		}
		// The old summary stays in the index until the pending one replaces it,
		// so a failed regeneration never loses it.
		w.Summary = idx.Weeks[i].Summary
		idx.Weeks[i] = w
		replaced = true
		break
	}
	if !replaced {
		idx.Weeks = append(idx.Weeks, w)
	}
	sort.SliceStable(idx.Weeks, func(i, j int) bool { return idx.Weeks[i].Start < idx.Weeks[j].Start })
	idx.Schema = SchemaVersion
	idx.UpdatedAt = now.Unix()
	return s.saveIndexLocked(idx)
}

// DeleteWeek removes one week file and its index entry.
func (s *Store) DeleteWeek(file string) error {
	if s.dir == "" {
		return nil
	}
	if !ValidWeekFile(file) {
		return ErrInvalidWeek
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, file)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrWeekNotFound
		}
		return fmt.Errorf("memory: 删除周文件: %w", err)
	}
	idx, err := s.loadIndexLocked(&Report{})
	if err != nil {
		return err
	}
	out := make([]Week, 0, len(idx.Weeks))
	for _, w := range idx.Weeks {
		if w.File != file {
			out = append(out, w)
		}
	}
	idx.Weeks = out
	idx.Schema = SchemaVersion
	idx.UpdatedAt = s.now().Unix()
	return s.saveIndexLocked(idx)
}

// Clear removes every week file and the index of this agent.
func (s *Store) Clear() error {
	if s.dir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.listWeekFilesLocked()
	if err != nil {
		return err
	}
	var errs []error
	for name := range files {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, name := range []string{SummaryFileName, SummaryFileName + corruptSuffix} {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("memory: 清除记忆: %w", errors.Join(errs...))
	}
	return nil
}

// Export writes every week as a readable markdown document.
func (s *Store) Export(w io.Writer) error {
	if s.dir == "" {
		return nil
	}
	s.mu.Lock()
	weeks, err := s.scanWeeksLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	contents := make([]string, len(weeks))
	for i, wk := range weeks {
		content, err := s.readWeekLocked(wk.File)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		contents[i] = content
	}
	s.mu.Unlock()

	if _, err := fmt.Fprintf(w, "# Agent 记忆导出\n\n导出时间：%s\n\n", s.now().Format("2006-01-02 15:04")); err != nil {
		return err
	}
	for i := len(weeks) - 1; i >= 0; i-- {
		wk := weeks[i]
		if _, err := fmt.Fprintf(w, "## %s ~ %s\n\n", wk.Start, wk.End); err != nil {
			return err
		}
		body := WeekBody(contents[i])
		if body == "" {
			body = "（这一周没有记录）"
		}
		if _, err := fmt.Fprintf(w, "%s\n\n", body); err != nil {
			return err
		}
	}
	return nil
}

// scanWeeksLocked merges the index with the week files on disk.
func (s *Store) scanWeeksLocked() ([]Week, error) {
	idx, err := s.loadIndexLocked(&Report{})
	if err != nil {
		return nil, err
	}
	files, err := s.listWeekFilesLocked()
	if err != nil {
		return nil, err
	}
	curKey := WeekKey(s.now())
	weeks := make(map[string]Week, len(files))
	for _, w := range idx.Weeks {
		if _, ok := files[w.File]; ok {
			weeks[w.File] = w
		}
	}
	for name, info := range files {
		w, ok := weeks[name]
		if ok {
			continue
		}
		start, end, _ := ParseWeekFile(name)
		w = Week{
			Start:     start.Format(weekLayout),
			End:       end.Format(weekLayout),
			File:      name,
			Status:    SummaryReady,
			UpdatedAt: info.ModTime().Unix(),
		}
		if w.Start < curKey {
			w.Status = SummaryPending
		}
		weeks[name] = w
	}
	return sortedWeeks(weeks), nil
}

func sortedWeeks(weeks map[string]Week) []Week {
	out := make([]Week, 0, len(weeks))
	for _, w := range weeks {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].File < out[j].File
	})
	return out
}

// listWeekFilesLocked returns the valid week files of the directory.
func (s *Store) listWeekFilesLocked() (map[string]os.FileInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]os.FileInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: 列出记忆文件: %w", err)
	}
	files := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !ValidWeekFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			log.Printf("memory: 读取 %s 信息: %v", entry.Name(), err)
			continue
		}
		if info.Size() > MaxWeekFileBytes {
			log.Printf("memory: 跳过超限周文件 %s（%d 字节）", entry.Name(), info.Size())
			continue
		}
		files[entry.Name()] = info
	}
	return files, nil
}

// loadIndexLocked reads summary.json. A missing index is empty; a corrupt one
// is preserved as summary.json.corrupt and rebuilt from the week files.
func (s *Store) loadIndexLocked(rep *Report) (Index, error) {
	path := filepath.Join(s.dir, SummaryFileName)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Index{Schema: SchemaVersion}, nil
	}
	if err != nil {
		return Index{}, fmt.Errorf("memory: 读取记忆索引: %w", err)
	}
	if info.Size() > MaxIndexBytes {
		return s.preserveIndexLocked(rep, info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("memory: 读取记忆索引: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil || idx.Schema != SchemaVersion {
		return s.preserveIndexLocked(rep, 0)
	}
	return idx, nil
}

// preserveIndexLocked keeps a corrupt index next to the rebuilt one.
func (s *Store) preserveIndexLocked(rep *Report, size int64) (Index, error) {
	path := filepath.Join(s.dir, SummaryFileName)
	backup := path + corruptSuffix
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Index{}, fmt.Errorf("memory: 保留损坏索引: %w", err)
	}
	if err := os.Rename(path, backup); err != nil {
		return Index{}, fmt.Errorf("memory: 保留损坏索引: %w", err)
	}
	if size > 0 {
		log.Printf("memory: 索引超过 %d 字节，已保留为 %s", size, filepath.Base(backup))
	}
	if rep != nil {
		rep.Warning = "记忆索引损坏，已重建；原文件保留为 " + SummaryFileName + corruptSuffix
	}
	return Index{Schema: SchemaVersion}, nil
}

func (s *Store) saveIndexLocked(idx Index) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: 编码记忆索引: %w", err)
	}
	data = append(data, '\n')
	if err := writeFile(filepath.Join(s.dir, SummaryFileName), data, memFileMode); err != nil {
		return fmt.Errorf("memory: 保存记忆索引: %w", err)
	}
	return nil
}

// writeFile replaces path with data through a temporary file in the same
// directory, so a crash never leaves a half written index.
func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, memDirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("设置文件权限: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入文件: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("同步文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭文件: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("替换文件: %w", err)
	}
	temp = ""
	return nil
}

// setSummary stores the summary of one pending week. Weeks that no longer
// exist on disk are ignored.
func (s *Store) setSummary(file, summary string, status SummaryStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadIndexLocked(&Report{})
	if err != nil {
		return err
	}
	updated := false
	for i := range idx.Weeks {
		w := &idx.Weeks[i]
		if w.File != file {
			continue
		}
		w.Summary = summary
		w.Status = status
		w.UpdatedAt = s.now().Unix()
		updated = true
		break
	}
	if !updated {
		return nil
	}
	idx.Schema = SchemaVersion
	idx.UpdatedAt = s.now().Unix()
	return s.saveIndexLocked(idx)
}

// oneLine collapses whitespace so that a summary stays on one list line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
