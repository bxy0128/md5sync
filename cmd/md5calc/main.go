// md5calc 是秒传哈希计算器的命令行版本。
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"md5sync/core"
	"md5sync/internal/clip"
	"md5sync/internal/scan"
)

func usage() {
	fmt.Fprintf(os.Stderr, `秒传 MD5 计算器（命令行）

用法:
  md5calc [选项] <文件|文件夹|URL> [更多...]

选项:
  -t N        并发数，默认 min(4, CPU)
  -auth U:P   访问 WebDAV 时使用的 Basic 认证
  -o FILE     额外把 JSON 写入文件
  -clip       从剪贴板读取待处理路径（每行一个）

不带参数且未指定 -clip 时，也会尝试读取剪贴板。
支持通配符（*.mkv）与文件夹递归。
结果以 JSON 数组输出到标准输出，并复制到剪贴板。
`)
}

func main() {
	threads := flag.Int("t", 0, "并发数")
	auth := flag.String("auth", "", "Basic 认证 user:pass")
	outFile := flag.String("o", "", "JSON 输出文件")
	forceClip := flag.Bool("clip", false, "从剪贴板读取路径")
	flag.Usage = usage
	flag.Parse()

	raw := flag.Args()
	if len(raw) == 0 || *forceClip {
		text, err := clip.Read()
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取剪贴板失败: %v\n", err)
		} else if text != "" {
			for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
				line = strings.Trim(strings.TrimSpace(line), `"`)
				if line != "" {
					raw = append(raw, line)
				}
			}
		}
	}
	if len(raw) == 0 {
		usage()
		os.Exit(1)
	}

	targets, skipped := scan.Expand(raw, func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	})
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "跳过 %d 个无效路径\n", skipped)
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "没有可处理的内容。")
		os.Exit(1)
	}

	authHeader := ""
	if *auth != "" {
		authHeader = base64.StdEncoding.EncodeToString([]byte(*auth))
	}

	n := len(targets)
	if *threads <= 0 {
		*threads = runtime.NumCPU()
		if *threads > 4 {
			*threads = 4
		}
	}
	if *threads > n {
		*threads = n
	}

	results := make([]core.Result, n)
	errs := make([]error, n)
	done := make([]int64, n)
	var finishedCount int64

	start := time.Now()
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var sumDone int64
				for i := 0; i < n; i++ {
					sumDone += atomic.LoadInt64(&done[i])
				}
				finished := atomic.LoadInt64(&finishedCount)
				spd := float64(sumDone) / (1 << 20) / time.Since(start).Seconds()
				fmt.Fprintf(os.Stderr, "\r已完成 %d/%d   已读 %s   平均 %.1f MB/s   ",
					finished, n, humanSize(sumDone), spd)
			case <-stopProgress:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	sem := make(chan struct{}, *threads)
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
			}, 300*time.Millisecond)
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
	close(stopProgress)
	fmt.Fprintln(os.Stderr)

	var okResults []core.Result
	failCount := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			failCount++
			fmt.Fprintf(os.Stderr, "失败: %s -> %v\n", targets[i].Name, errs[i])
			continue
		}
		okResults = append(okResults, results[i])
	}
	if len(okResults) == 0 {
		fmt.Fprintln(os.Stderr, "没有成功的结果。")
		os.Exit(1)
	}

	payload, err := json.MarshalIndent(okResults, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成 JSON 失败: %v\n", err)
		os.Exit(1)
	}
	text := string(payload)

	fmt.Println(text)
	if *outFile != "" {
		if err := os.WriteFile(*outFile, []byte(text), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "写入文件失败: %v\n", err)
		}
	}
	if err := clip.Write(text); err != nil {
		fmt.Fprintf(os.Stderr, "复制到剪贴板失败: %v\n", err)
	}

	var totalBytes int64
	for _, r := range okResults {
		totalBytes += r.Size
	}
	elapsed := time.Since(start).Seconds()
	fmt.Fprintf(os.Stderr, "总耗时 %.1f 秒   文件数 %d   平均 %.0f MB/s   已复制到剪贴板\n",
		elapsed, len(okResults), float64(totalBytes)/(1<<20)/elapsed)

	if failCount > 0 {
		os.Exit(2)
	}
}

func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}
