// Package core 负责秒传所需的哈希计算。
//
// 规则依据天翼开放平台文档 api_detail_716：
//   - 按文件大小查表得到分片大小 sliceSize
//   - 每片单独算 MD5（大写十六进制），用换行拼接后再算一次 MD5 得到 slice_md5
//   - 文件不超过 10MB 时 slice_md5 直接等于整文件 MD5
package core

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// MiB 是 1024*1024，文档中的分片单位。
const MiB int64 = 1024 * 1024

// SmallLimit 以下的文件，slice_md5 等于整文件 MD5。
const SmallLimit int64 = 10 * MiB

// Result 是一条可直接被网页端秒传助手识别的记录。
type Result struct {
	MD5      string `json:"md5"`
	SliceMD5 string `json:"slice_md5"`
	Size     int64  `json:"size"`
	Name     string `json:"name"`
	Cloud    string `json:"cloud"`
}

// Progress 用于向界面回传进度。
type Progress struct {
	Index  int
	Done   int64
	Total  int64
	Source string
}

// SliceSize 按文件大小计算分片大小。
func SliceSize(size int64) int64 {
	switch {
	case size < 1000*10*MiB:
		return 10 * MiB
	case size < 1000*20*MiB:
		return 20 * MiB
	case size < 2000*50*MiB:
		return 50 * MiB
	}
	n := size/(2000*10*MiB) + 1
	return n * 10 * MiB
}

const (
	// chunkSize 是单次读取的缓冲大小。
	chunkSize = 4 * MiB
	// chunkPoolSize 个缓冲轮流复用，内存占用固定（约 16 MB）。
	chunkPoolSize = 4
)

// chunk 是一段已读入内存的数据；整文件、分片两个消费者都处理完后才归还缓冲。
type chunk struct {
	buf  []byte
	n    int
	left *int32
}

func releaseChunk(pool chan []byte, c *chunk) {
	if atomic.AddInt32(c.left, -1) == 0 {
		pool <- c.buf
	}
}

// HashReader 边读边算，不缓存文件内容。
//
// 整文件 MD5 与分片 MD5 由两个 goroutine 消费同一批数据：文件只读一遍，
// 两份哈希并行计算，避免单线程里先后算两次导致的 CPU 瓶颈。
func HashReader(r io.Reader, size int64, report func(done, total int64)) (string, string, error) {
	sliceSize := SliceSize(size)
	small := size <= SmallLimit

	pool := make(chan []byte, chunkPoolSize)
	for i := 0; i < chunkPoolSize; i++ {
		pool <- make([]byte, chunkSize)
	}

	wholeCh := make(chan *chunk, chunkPoolSize)
	sliceCh := make(chan *chunk, chunkPoolSize)

	// 消费者 1：整文件 MD5
	wholeDone := make(chan string, 1)
	go func() {
		h := md5.New()
		for c := range wholeCh {
			h.Write(c.buf[:c.n])
			releaseChunk(pool, c)
		}
		wholeDone <- strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	}()

	// 消费者 2：分片 MD5；小文件直接释放缓冲
	sliceDone := make(chan []string, 1)
	go func() {
		var parts []string
		if !small {
			h := md5.New()
			remain := sliceSize
			for c := range sliceCh {
				off := 0
				for off < c.n {
					take := int64(c.n - off)
					if take > remain {
						take = remain
					}
					h.Write(c.buf[off : off+int(take)])
					remain -= take
					off += int(take)
					if remain == 0 {
						parts = append(parts, strings.ToUpper(hex.EncodeToString(h.Sum(nil))))
						h.Reset()
						remain = sliceSize
					}
				}
				releaseChunk(pool, c)
			}
			// 最后一片不足 sliceSize 也要计入
			if remain != sliceSize {
				parts = append(parts, strings.ToUpper(hex.EncodeToString(h.Sum(nil))))
			}
		} else {
			for c := range sliceCh {
				releaseChunk(pool, c)
			}
		}
		sliceDone <- parts
	}()

	readErr := make(chan error, 1)
	var done int64
	go func() {
		defer close(sliceCh)
		defer close(wholeCh)
		for {
			buf := <-pool
			n, err := r.Read(buf)
			if n > 0 {
				left := int32(2)
				c := &chunk{buf: buf, n: n, left: &left}
				wholeCh <- c
				sliceCh <- c
				done += int64(n)
				if report != nil {
					report(done, size)
				}
			} else {
				pool <- buf
			}
			if err != nil {
				if err == io.EOF {
					readErr <- nil
				} else {
					readErr <- err
				}
				return
			}
		}
	}()

	err := <-readErr
	full := <-wholeDone
	parts := <-sliceDone
	if err != nil {
		return "", "", err
	}

	if done != size {
		return "", "", fmt.Errorf("读取字节数与文件大小不一致（期望 %d，实际 %d）", size, done)
	}

	if small || len(parts) == 0 {
		return full, full, nil
	}
	agg := md5.Sum([]byte(strings.Join(parts, "\n")))
	return full, strings.ToUpper(hex.EncodeToString(agg[:])), nil
}

// HashFile 计算本地文件。
func HashFile(path string, report func(done, total int64)) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	if st.IsDir() {
		return Result{}, errors.New("这是文件夹")
	}

	m, s, err := HashReader(f, st.Size(), report)
	if err != nil {
		return Result{}, err
	}
	return Result{MD5: m, SliceMD5: s, Size: st.Size(), Name: filepath.Base(path), Cloud: "189"}, nil
}

var httpClient = &http.Client{Timeout: 0}

// HashURL 通过 HTTP(S) 流式计算，不落盘。
func HashURL(rawURL, authBase64 string, report func(done, total int64)) (Result, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "md5sync/1.0")
	if authBase64 != "" {
		req.Header.Set("Authorization", "Basic "+authBase64)
	}
	// 用 GET 而不是 HEAD，部分 WebDAV 服务不支持 HEAD
	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength <= 0 {
		return Result{}, errors.New("服务器未返回 Content-Length，无法确定置分片大小")
	}

	m, s, err := HashReader(resp.Body, resp.ContentLength, report)
	if err != nil {
		return Result{}, err
	}
	name := rawURL
	if i := strings.LastIndexAny(name, "/?"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "download"
	}
	return Result{MD5: m, SliceMD5: s, Size: resp.ContentLength, Name: name, Cloud: "189"}, nil
}

// Throttle 把高频进度回调压到 interval 一次。
func Throttle(report func(done, total int64), interval time.Duration) func(int64, int64) {
	var last time.Time
	return func(done, total int64) {
		now := time.Now()
		if last.IsZero() || now.Sub(last) >= interval || done >= total {
			last = now
			report(done, total)
		}
	}
}
