package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"md5sync/core"
	"md5sync/internal/clip"
	"md5sync/internal/scan"
)

// App 是暴露给前端的后端对象，所有方法都会绑定到 window.go.main.App。
type App struct {
	ctx context.Context

	mu       sync.Mutex
	running  bool
	jsonText string
	results  []core.Result
}

// NewApp 创建应用实例。
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// PickFiles 打开文件选择框，返回选中的文件。
func (a *App) PickFiles() []string {
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择要计算的文件",
	})
	if err != nil {
		return nil
	}
	return paths
}

// PickFolder 打开文件夹选择框，递归返回其中所有文件。
func (a *App) PickFolder() []string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择文件夹（会递归收集其中的文件）",
	})
	if err != nil || dir == "" {
		return nil
	}
	targets, _ := scan.Expand([]string{dir}, nil)
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Src)
	}
	return out
}

// Clipboard 读取剪贴板文本（每行一个路径或直链）。
func (a *App) Clipboard() string {
	text, err := clip.Read()
	if err != nil {
		return ""
	}
	return text
}

// Start 开始计算，立即返回展开后的文件名列表，真正的进度通过事件回传。
func (a *App) Start(paths []string, threads int, auth string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("没有待计算的内容")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, errors.New("正在计算中，请等待完成")
	}
	targets, _ := scan.Expand(paths, nil)
	if len(targets) == 0 {
		a.mu.Unlock()
		return nil, errors.New("没有可处理的文件")
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	a.running = true
	a.results = nil
	a.jsonText = ""
	a.mu.Unlock()

	authHeader := ""
	if auth != "" {
		authHeader = base64.StdEncoding.EncodeToString([]byte(auth))
	}

	go a.run(targets, threads, authHeader)
	return names, nil
}

func (a *App) run(targets []scan.Target, threads int, authHeader string) {
	n := len(targets)
	if threads < 1 {
		threads = 1
	}
	if threads > 8 {
		threads = 8
	}
	if threads > n {
		threads = n
	}

	done := make([]int64, n)
	totals := make([]int64, n)
	results := make([]core.Result, n)
	errs := make([]error, n)
	var finishedCount int64

	start := time.Now()
	stop := make(chan struct{})

	// 每 200ms 向前端汇总一次进度。
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var bytes int64
				percent := make([]int, n)
				for i := 0; i < n; i++ {
					d := atomic.LoadInt64(&done[i])
					t := atomic.LoadInt64(&totals[i])
					bytes += d
					if t > 0 {
						p := int(d * 100 / t)
						if p > 100 {
							p = 100
						}
						percent[i] = p
					} else if atomic.LoadInt64(&done[i]) > 0 {
						percent[i] = -1
					}
				}
				elapsed := time.Since(start).Seconds()
				speed := 0.0
				if elapsed > 0 {
					speed = float64(bytes) / (1 << 20) / elapsed
				}
				runtime.EventsEmit(a.ctx, "progress", map[string]any{
					"finished": atomic.LoadInt64(&finishedCount),
					"count":    n,
					"percent":  percent,
					"speed":    speed,
					"bytes":    bytes,
					"elapsed":  elapsed,
				})
			case <-stop:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	sem := make(chan struct{}, threads)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			t := targets[i]
			report := core.Throttle(func(d, total int64) {
				atomic.StoreInt64(&done[i], d)
				atomic.StoreInt64(&totals[i], total)
			}, 200*time.Millisecond)

			var (
				res core.Result
				err error
			)
			if scan.IsURL(t.Src) {
				res, err = core.HashURL(t.Src, authHeader, report)
			} else {
				res, err = core.HashFile(t.Src, report)
			}
			if err != nil {
				errs[i] = err
				atomic.AddInt64(&finishedCount, 1)
				return
			}
			results[i] = res
			atomic.AddInt64(&finishedCount, 1)
		}()
	}
	wg.Wait()
	close(stop)

	var ok []core.Result
	failed := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			failed++
			runtime.EventsEmit(a.ctx, "file-fail", map[string]any{
				"index":   i,
				"message": errs[i].Error(),
			})
			continue
		}
		ok = append(ok, results[i])
		runtime.EventsEmit(a.ctx, "file-done", map[string]any{
			"index":  i,
			"result": results[i],
		})
	}

	jsonText := ""
	if len(ok) > 0 {
		if payload, err := json.MarshalIndent(ok, "", "  "); err == nil {
			jsonText = string(payload)
			// 与命令行版一致：算完直接放进剪贴板，去网页粘贴即可。
			_ = clip.Write(jsonText)
		}
	}

	a.mu.Lock()
	a.running = false
	a.results = ok
	a.jsonText = jsonText
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "done", map[string]any{
		"json":    jsonText,
		"ok":      len(ok),
		"failed":  failed,
		"elapsed": time.Since(start).Seconds(),
	})
}

// CopyJSON 把结果复制到剪贴板。
func (a *App) CopyJSON() error {
	a.mu.Lock()
	text := a.jsonText
	a.mu.Unlock()
	if text == "" {
		return errors.New("还没有可复制的结果")
	}
	return clip.Write(text)
}

// SaveJSON 让用户选择位置保存结果。
func (a *App) SaveJSON() (string, error) {
	a.mu.Lock()
	text := a.jsonText
	a.mu.Unlock()
	if text == "" {
		return "", errors.New("还没有可保存的结果")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存秒传 JSON",
		DefaultFilename: "秒传结果.json",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON 文件 (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	return path, nil
}
