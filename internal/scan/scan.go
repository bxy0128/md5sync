// Package scan 负责把用户给的路径（文件、文件夹、通配符、URL）展开成待处理列表。
package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Target 是一个待计算的来源。
type Target struct {
	Src  string
	Name string
}

// IsURL 判断是否为 http(s) 地址。
func IsURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// URLName 从地址里取出文件名。
func URLName(raw string) string {
	name := raw
	if i := strings.LastIndexAny(name, "/?"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = raw
	}
	return name
}

// Expand 展开通配符与文件夹，保持输入顺序并去重。
func Expand(raw []string, warn func(string)) ([]Target, int) {
	var out []Target
	skipped := 0
	seen := make(map[string]bool)
	add := func(src, name string) {
		key := strings.ToLower(src)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Target{Src: src, Name: name})
	}
	say := func(msg string) {
		skipped++
		if warn != nil {
			warn(msg)
		}
	}

	for _, p := range raw {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p == "" {
			continue
		}
		if IsURL(p) {
			add(p, URLName(p))
			continue
		}

		matches := []string{p}
		if strings.ContainsAny(p, "*?") {
			matches, _ = filepath.Glob(p)
			if len(matches) == 0 {
				say(fmt.Sprintf("跳过（无匹配）: %s", p))
				continue
			}
		}

		for _, m := range matches {
			st, err := os.Stat(m)
			if err != nil {
				say(fmt.Sprintf("跳过（不存在）: %s", m))
				continue
			}
			if st.IsDir() {
				filepath.WalkDir(m, func(path string, d os.DirEntry, err error) error {
					if err != nil || d == nil || d.IsDir() {
						return nil
					}
					add(path, d.Name())
					return nil
				})
				continue
			}
			abs, _ := filepath.Abs(m)
			add(abs, filepath.Base(m))
		}
	}
	return out, skipped
}
