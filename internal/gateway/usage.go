package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Usage log limits.
const (
	UsageFileName = "usage.jsonl"
	usageMaxBytes = 5 << 20
	usageKeep     = 5
)

// UsageRecord is one line of usage.jsonl. It never holds prompts, replies,
// keys or URLs.
type UsageRecord struct {
	Time           int64      `json:"time"`
	Origin         string     `json:"origin"`
	ConversationID string     `json:"conversation_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	ProviderID     string     `json:"provider_id"`
	ProviderName   string     `json:"provider_name"`
	Model          string     `json:"model"`
	Capability     Capability `json:"capability"`
	InputTokens    int        `json:"input_tokens"`
	OutputTokens   int        `json:"output_tokens"`
	UsageEstimated bool       `json:"usage_estimated"`
	DurationMS     int64      `json:"duration_ms"`
	Status         string     `json:"status"`
	ErrorCode      ErrorKind  `json:"error_code,omitempty"`
}

// Usage statuses.
const (
	UsageOK    = "ok"
	UsageError = "error"
)

// UsageLog appends usage records to a JSON Lines file and rotates it.
type UsageLog struct {
	dir      string
	path     string
	mu       sync.Mutex
	maxBytes int64
	keep     int
	now      func() time.Time
}

// NewUsageLog returns the log of a gateway directory.
func NewUsageLog(dir string) *UsageLog {
	return &UsageLog{
		dir:      dir,
		path:     filepath.Join(dir, UsageFileName),
		maxBytes: usageMaxBytes,
		keep:     usageKeep,
		now:      time.Now,
	}
}

// Path returns the current usage file path.
func (u *UsageLog) Path() string { return u.path }

// Append adds one record, rotating first when the file grew past the limit.
func (u *UsageLog) Append(rec UsageRecord) error {
	if rec.Time == 0 {
		rec.Time = u.now().Unix()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("gateway: encode usage: %w", err)
	}
	data = append(data, '\n')
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := os.MkdirAll(u.dir, gatewayDirMode); err != nil {
		return fmt.Errorf("gateway: create usage directory: %w", err)
	}
	if err := u.rotateLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(u.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, gatewayFileMode)
	if err != nil {
		return fmt.Errorf("gateway: open usage log: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("gateway: append usage: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("gateway: close usage log: %w", err)
	}
	return nil
}

func (u *UsageLog) rotateLocked() error {
	info, err := os.Stat(u.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("gateway: stat usage log: %w", err)
	}
	if info.Size() < u.maxBytes {
		return nil
	}
	target := filepath.Join(u.dir, fmt.Sprintf("usage-%d.jsonl", u.now().Unix()))
	if _, err := os.Stat(target); err == nil {
		target = filepath.Join(u.dir, fmt.Sprintf("usage-%d.jsonl", u.now().UnixNano()))
	}
	if err := os.Rename(u.path, target); err != nil {
		return fmt.Errorf("gateway: rotate usage log: %w", err)
	}
	return u.pruneLocked()
}

func (u *UsageLog) pruneLocked() error {
	files, err := filepath.Glob(filepath.Join(u.dir, "usage-*.jsonl"))
	if err != nil {
		return fmt.Errorf("gateway: list usage logs: %w", err)
	}
	sort.Strings(files)
	for len(files) > u.keep {
		if err := os.Remove(files[0]); err != nil {
			return fmt.Errorf("gateway: remove old usage log: %w", err)
		}
		files = files[1:]
	}
	return nil
}

// Recent returns the last n records, newest first.
func (u *UsageLog) Recent(n int) ([]UsageRecord, error) {
	if n <= 0 {
		return nil, nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	lines, err := u.readLines()
	if err != nil {
		return nil, err
	}
	out := make([]UsageRecord, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		var rec UsageRecord
		if err := json.Unmarshal(lines[i], &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// Count returns the number of readable records in the current file.
func (u *UsageLog) Count() (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	lines, err := u.readLines()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range lines {
		var rec UsageRecord
		if json.Unmarshal(line, &rec) == nil {
			count++
		}
	}
	return count, nil
}

func (u *UsageLog) readLines() ([][]byte, error) {
	data, err := os.ReadFile(u.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("gateway: read usage log: %w", err)
	}
	var lines [][]byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("gateway: read usage log: %w", err)
	}
	return lines, nil
}

// UsageStatus is the status field of a successful call.
func usageStatus(err error) string {
	if err != nil {
		return UsageError
	}
	return UsageOK
}

func usageErrorCode(err error) ErrorKind {
	if err == nil {
		return ""
	}
	return KindOf(err)
}
