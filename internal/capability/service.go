// Package capability 实现 WebApp 的统一能力集：私有目录、文件访问、剪贴板、
// 通知与外链，并按调用窗口识别应用身份。
package capability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Limits of the capability API.
const (
	// MaxTextSize is the largest text read or written at once, in bytes.
	MaxTextSize = 1 << 20
	// MaxListEntries is the largest directory listing returned.
	MaxListEntries = 500
	// MaxGrantsPerApp is the number of file grants one window may hold.
	MaxGrantsPerApp = 32
	// MaxNotifyTitle and MaxNotifyBody bound a notification, in characters.
	MaxNotifyTitle = 64
	MaxNotifyBody  = 256
)

// Capability error codes returned to pages.
const (
	CodeUnauthorized    = "unauthorized"
	CodeInvalidArgument = "invalid_argument"
	CodeOutOfRoot       = "out_of_root"
	CodeNotFound        = "not_found"
	CodeTooLarge        = "too_large"
	CodeDenied          = "denied"
	CodeUnsupported     = "unsupported"
	CodeInternal        = "internal"
)

// Reply is the envelope every capability method returns. Pages never see Go
// errors: a failure carries a code and a message instead.
type Reply struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func okReply(data any) Reply {
	return Reply{OK: true, Data: data}
}

func errorReply(code, message string) Reply {
	return Reply{OK: false, Code: code, Message: message}
}

// Platform is the host side of the capabilities. Tests replace it with fakes;
// the app wires the mygo implementation.
type Platform struct {
	PickFile       func(ctx context.Context) (string, error)
	PickDirectory  func(ctx context.Context) (string, error)
	ReadClipboard  func() (string, error)
	WriteClipboard func(text string) error
	Notify         func(title, body string) error
	SupportsNotify func() bool
	OpenExternal   func(target string) error
}

// Service is bound as BowknotCapability. Identity resolves the app ID of the
// calling window, so a page cannot claim to be another app.
type Service struct {
	// AppDataDir is {DataDir}/appdata.
	AppDataDir string
	// Identify returns the app ID that owns the calling window.
	Identify func(ctx context.Context) (string, bool)
	Platform Platform

	mu     sync.Mutex
	grants map[string][]grant
}

type grant struct {
	token       string
	root        string
	isDirectory bool
}

type readResult struct {
	Content string `json:"content"`
}

type entryInfo struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
}

type listResult struct {
	Entries []entryInfo `json:"entries"`
}

type pickResult struct {
	Token       string `json:"token"`
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
}

// caller returns the app ID of the calling window, or an unauthorized reply.
func (s *Service) caller(ctx context.Context) (string, Reply) {
	if s.Identify == nil {
		return "", errorReply(CodeUnauthorized, "无法识别调用应用")
	}
	id, ok := s.Identify(ctx)
	if !ok || id == "" {
		return "", errorReply(CodeUnauthorized, "无法识别调用应用")
	}
	return id, Reply{OK: true}
}

// appRoot returns the private data root of an app.
func (s *Service) appRoot(id string) string {
	return filepath.Join(s.AppDataDir, id)
}

// resolveAppData validates rel and returns the target inside the app root.
func (s *Service) resolveAppData(id, rel string) (string, bool) {
	root := s.appRoot(id)
	target := joinRoot(root, rel)
	if !withinRoot(root, target) {
		return "", false
	}
	return target, true
}

// ForgetWindow drops the grants of an app window, so tokens stop working as
// soon as the window closes.
func (s *Service) ForgetWindow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.grants, id)
}

// AppDataRead returns a UTF-8 text file from the app private directory.
func (s *Service) AppDataRead(ctx context.Context, path string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	rel, err := cleanRelativePath(path)
	if err != nil {
		return errorReply(CodeInvalidArgument, err.Error())
	}
	target, ok := s.resolveAppData(id, rel)
	if !ok {
		return errorReply(CodeOutOfRoot, "路径超出应用私有目录")
	}
	return readTextFile(target)
}

// AppDataWrite writes a UTF-8 text file to the app private directory,
// creating missing folders.
func (s *Service) AppDataWrite(ctx context.Context, path, text string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	if len(text) > MaxTextSize {
		return errorReply(CodeTooLarge, "文本超过 1 MiB")
	}
	rel, err := cleanRelativePath(path)
	if err != nil {
		return errorReply(CodeInvalidArgument, err.Error())
	}
	target, ok := s.resolveAppData(id, rel)
	if !ok {
		return errorReply(CodeOutOfRoot, "路径超出应用私有目录")
	}
	if err := writeTextFile(target, text); err != nil {
		return errorReply(CodeInternal, "写入文件失败")
	}
	return okReply(nil)
}

// AppDataList lists a folder in the app private directory; an empty path is
// the root.
func (s *Service) AppDataList(ctx context.Context, path string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	rel, err := cleanPathOrRoot(path)
	if err != nil {
		return errorReply(CodeInvalidArgument, err.Error())
	}
	target, ok := s.resolveAppData(id, rel)
	if !ok {
		return errorReply(CodeOutOfRoot, "路径超出应用私有目录")
	}
	return listDirectory(target)
}

// AppDataRemove deletes a file or folder in the app private directory.
func (s *Service) AppDataRemove(ctx context.Context, path string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	rel, err := cleanRelativePath(path)
	if err != nil {
		return errorReply(CodeInvalidArgument, err.Error())
	}
	target, ok := s.resolveAppData(id, rel)
	if !ok {
		return errorReply(CodeOutOfRoot, "路径超出应用私有目录")
	}
	if _, err := os.Lstat(target); err != nil {
		return errorReply(CodeNotFound, "文件不存在")
	}
	if err := os.RemoveAll(target); err != nil {
		return errorReply(CodeInternal, "删除失败")
	}
	return okReply(nil)
}

// PickFile opens the system file picker and returns a grant token.
func (s *Service) PickFile(ctx context.Context) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	return s.pick(ctx, id, false)
}

// PickDirectory opens the system folder picker and returns a grant token.
func (s *Service) PickDirectory(ctx context.Context) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	return s.pick(ctx, id, true)
}

func (s *Service) pick(ctx context.Context, id string, directory bool) Reply {
	var path string
	var err error
	if directory {
		if s.Platform.PickDirectory == nil {
			return errorReply(CodeUnsupported, "当前平台不支持选择目录")
		}
		path, err = s.Platform.PickDirectory(ctx)
	} else {
		if s.Platform.PickFile == nil {
			return errorReply(CodeUnsupported, "当前平台不支持选择文件")
		}
		path, err = s.Platform.PickFile(ctx)
	}
	if err != nil {
		return errorReply(CodeInternal, "打开选择器失败")
	}
	if path == "" {
		return okReply(nil)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return errorReply(CodeInvalidArgument, "选择无效")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return errorReply(CodeInvalidArgument, "选择无效")
	}
	if directory && !info.IsDir() {
		return errorReply(CodeInvalidArgument, "请选择目录")
	}
	if !directory && !info.Mode().IsRegular() {
		return errorReply(CodeInvalidArgument, "请选择文件")
	}
	g, err := s.addGrant(id, abs, directory)
	if err != nil {
		return errorReply(CodeTooLarge, "授权数量已达上限")
	}
	return okReply(pickResult{Token: g.token, Name: filepath.Base(abs), IsDirectory: directory})
}

// ReadGranted reads a text file from a previously picked file or folder.
func (s *Service) ReadGranted(ctx context.Context, token, relative string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	target, reply := s.grantedPath(id, token, relative)
	if !reply.OK {
		return reply
	}
	return readTextFile(target)
}

// WriteGranted writes a text file inside a previously picked file or folder.
func (s *Service) WriteGranted(ctx context.Context, token, relative, text string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	if len(text) > MaxTextSize {
		return errorReply(CodeTooLarge, "文本超过 1 MiB")
	}
	g, ok := s.findGrant(id, token)
	if !ok {
		return errorReply(CodeNotFound, "授权已失效")
	}
	target, reply := grantTarget(g, relative)
	if !reply.OK {
		return reply
	}
	if !g.isDirectory {
		if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
			return errorReply(CodeInvalidArgument, "目标不是文件")
		}
	} else if rel, _ := cleanPathOrRoot(relative); rel == "." {
		return errorReply(CodeInvalidArgument, "请指定文件名")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return errorReply(CodeInternal, "创建目录失败")
	}
	if err := writeTextFile(target, text); err != nil {
		return errorReply(CodeInternal, "写入文件失败")
	}
	return okReply(nil)
}

// ListGranted lists a folder inside a previously picked folder.
func (s *Service) ListGranted(ctx context.Context, token, relative string) Reply {
	id, r := s.caller(ctx)
	if !r.OK {
		return r
	}
	g, ok := s.findGrant(id, token)
	if !ok {
		return errorReply(CodeNotFound, "授权已失效")
	}
	if !g.isDirectory {
		return errorReply(CodeInvalidArgument, "文件授权不能列出目录")
	}
	target, reply := grantTarget(g, relative)
	if !reply.OK {
		return reply
	}
	return listDirectory(target)
}

// ReadClipboardText returns the clipboard text.
func (s *Service) ReadClipboardText(ctx context.Context) Reply {
	if _, r := s.caller(ctx); !r.OK {
		return r
	}
	if s.Platform.ReadClipboard == nil {
		return errorReply(CodeUnsupported, "当前平台不支持剪贴板")
	}
	text, err := s.Platform.ReadClipboard()
	if err != nil {
		return errorReply(CodeInternal, "读取剪贴板失败")
	}
	if len(text) > MaxTextSize {
		return errorReply(CodeTooLarge, "剪贴板文本超过 1 MiB")
	}
	return okReply(readResult{Content: text})
}

// WriteClipboardText replaces the clipboard text.
func (s *Service) WriteClipboardText(ctx context.Context, text string) Reply {
	if _, r := s.caller(ctx); !r.OK {
		return r
	}
	if len(text) > MaxTextSize {
		return errorReply(CodeTooLarge, "文本超过 1 MiB")
	}
	if s.Platform.WriteClipboard == nil {
		return errorReply(CodeUnsupported, "当前平台不支持剪贴板")
	}
	if err := s.Platform.WriteClipboard(text); err != nil {
		return errorReply(CodeInternal, "写入剪贴板失败")
	}
	return okReply(nil)
}

// Notify shows a system notification.
func (s *Service) Notify(ctx context.Context, title, body string) Reply {
	if _, r := s.caller(ctx); !r.OK {
		return r
	}
	if s.Platform.SupportsNotify != nil && !s.Platform.SupportsNotify() {
		return errorReply(CodeUnsupported, "系统通知不可用")
	}
	if s.Platform.Notify == nil {
		return errorReply(CodeUnsupported, "系统通知不可用")
	}
	if utf8.RuneCountInString(title) > MaxNotifyTitle {
		return errorReply(CodeInvalidArgument, "标题不能超过 64 个字符")
	}
	if utf8.RuneCountInString(body) > MaxNotifyBody {
		return errorReply(CodeInvalidArgument, "正文不能超过 256 个字符")
	}
	if err := s.Platform.Notify(title, body); err != nil {
		return errorReply(CodeInternal, "发送通知失败")
	}
	return okReply(nil)
}

// OpenExternal opens an http, https or mailto link in the system browser.
func (s *Service) OpenExternal(ctx context.Context, target string) Reply {
	if _, r := s.caller(ctx); !r.OK {
		return r
	}
	u, err := url.Parse(target)
	if err != nil {
		return errorReply(CodeInvalidArgument, "链接无效")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return errorReply(CodeInvalidArgument, "链接无效")
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return errorReply(CodeInvalidArgument, "链接无效")
		}
	default:
		return errorReply(CodeDenied, "只允许打开 http、https 与 mailto 链接")
	}
	if s.Platform.OpenExternal == nil {
		return errorReply(CodeUnsupported, "当前平台不支持打开链接")
	}
	if err := s.Platform.OpenExternal(target); err != nil {
		return errorReply(CodeInternal, "打开链接失败")
	}
	return okReply(nil)
}

// addGrant records a picked file or folder for the app window.
func (s *Service) addGrant(id, root string, directory bool) (grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants == nil {
		s.grants = make(map[string][]grant)
	}
	list := s.grants[id]
	if len(list) >= MaxGrantsPerApp {
		return grant{}, fmt.Errorf("grant limit reached")
	}
	g := grant{token: newToken(), root: root, isDirectory: directory}
	s.grants[id] = append(list, g)
	return g, nil
}

func (s *Service) findGrant(id, token string) (grant, bool) {
	if token == "" {
		return grant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.grants[id] {
		if g.token == token {
			return g, true
		}
	}
	return grant{}, false
}

// grantedPath resolves a relative path inside a grant.
func (s *Service) grantedPath(id, token, relative string) (string, Reply) {
	g, ok := s.findGrant(id, token)
	if !ok {
		return "", errorReply(CodeNotFound, "授权已失效")
	}
	return grantTarget(g, relative)
}

// grantTarget maps a relative path to a path inside the grant root. A file
// grant accepts only the root itself.
func grantTarget(g grant, relative string) (string, Reply) {
	if !g.isDirectory {
		if relative != "" && relative != "." {
			return "", errorReply(CodeOutOfRoot, "路径超出授权范围")
		}
		return g.root, Reply{OK: true}
	}
	rel, err := cleanPathOrRoot(relative)
	if err != nil {
		return "", errorReply(CodeInvalidArgument, err.Error())
	}
	target := joinRoot(g.root, rel)
	if !withinRoot(g.root, target) {
		return "", errorReply(CodeOutOfRoot, "路径超出授权范围")
	}
	return target, Reply{OK: true}
}

// readTextFile reads a UTF-8 text file with the size limit.
func readTextFile(path string) Reply {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errorReply(CodeNotFound, "文件不存在")
	}
	if info.Size() > MaxTextSize {
		return errorReply(CodeTooLarge, "文件超过 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errorReply(CodeInternal, "读取文件失败")
	}
	if !utf8.Valid(data) {
		return errorReply(CodeInvalidArgument, "文件不是 UTF-8 文本")
	}
	return okReply(readResult{Content: string(data)})
}

// writeTextFile writes a UTF-8 text file, creating its folder.
func writeTextFile(path, text string) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("target is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// listDirectory lists regular files and folders, sorted by name.
func listDirectory(path string) Reply {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return okReply(listResult{Entries: []entryInfo{}})
		}
		return errorReply(CodeNotFound, "目录不存在")
	}
	if len(entries) > MaxListEntries {
		return errorReply(CodeTooLarge, "目录条目超过 500")
	}
	out := make([]entryInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		size := info.Size()
		if entry.IsDir() {
			size = 0
		}
		out = append(out, entryInfo{Name: entry.Name(), IsDirectory: entry.IsDir(), Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return okReply(listResult{Entries: out})
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
