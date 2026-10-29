package plugin

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// InjectionBudgetRunes bounds the rune count of the plugin section injected
// into the system prompt, about 3000 tokens of mixed Chinese text.
const InjectionBudgetRunes = 6000

// SkillSection assembles the prompt section of the enabled plugins in list
// order: the skill files first, then the schemas of the tools the plugins
// declare and the host executes. A plugin whose directory no longer validates
// is skipped and reported through invalid, so the caller can remove it from
// the enable list and prompt the user to re-import it.
func (s *Store) SkillSection(ids []string) (section, notice string, invalid []string) {
	var b strings.Builder
	tools := map[string]bool{}
	wrote := false
	for _, id := range ids {
		e, ok := s.Get(id)
		if !ok {
			invalid = append(invalid, id)
			notice += fmt.Sprintf("插件 %s 不存在，已从启用清单移除，请重新导入；", id)
			continue
		}
		text, err := s.skillText(e)
		if err != nil {
			invalid = append(invalid, id)
			notice += fmt.Sprintf("插件 %s 校验失败，已从启用清单移除，请重新导入；", e.Name)
			continue
		}
		if !wrote {
			b.WriteString("## 插件技能\n\n")
			b.WriteString("以下是已启用插件提供的技能与工具，只能在声明范围内使用。\n\n")
			wrote = true
		}
		b.WriteString("### " + e.Name + "（" + e.ID + "）\n\n")
		b.WriteString(text)
		b.WriteString("\n\n")
		for _, tool := range e.Tools {
			tools[tool] = true
		}
	}
	if wrote {
		if schemas := ToolSchemas(mapKeys(tools)); len(schemas) > 0 {
			b.WriteString("### 可用工具\n\n")
			for _, schema := range schemas {
				b.WriteString("- `" + schema.Name + "`：" + schema.Description + " 参数：" + schema.Params + "\n")
			}
		}
	}
	section = strings.TrimRight(b.String(), "\n")
	if runes := utf8.RuneCountInString(section); runes > InjectionBudgetRunes {
		section = truncateRunes(section, InjectionBudgetRunes) + "\n（插件内容超出预算，已截断）"
	}
	return section, notice, invalid
}

// skillText reads every skill file of one installed plugin in manifest order.
func (s *Store) skillText(e Entry) (string, error) {
	if _, err := s.Manifest(e.ID); err != nil {
		return "", err
	}
	dir := s.PluginDir(e.ID)
	parts := make([]string, 0, len(e.Skills))
	for _, skill := range e.Skills {
		path, err := resolveInRoot(dir, skill)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("skill %s is missing", skill)
		}
		if info.Size() > MaxSkillSize {
			return "", fmt.Errorf("skill %s exceeds %d bytes", skill, MaxSkillSize)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read skill %s: %w", skill, err)
		}
		if !utf8.Valid(data) {
			return "", fmt.Errorf("skill %s is not valid UTF-8", skill)
		}
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n\n"), nil
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// truncateRunes cuts text to at most n runes.
func truncateRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}
