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
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// HashReader 边读边算，不缓存文件内容。
func HashReader(r io.Reader, size int64, report func(done, total int64)) (string, string, error) {
	sliceSize := SliceSize(size)
	small := size <= SmallLimit

	whole := md5.New()
	buf := make([]byte, 4*MiB)

	var parts []string
	var part hash.Hash
	var partRemain int64
	if !small {
		part = md5.New()
		partRemain = sliceSize
	}

	var done int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			whole.Write(buf[:n])
			if !small {
				off := 0
				for off < n {
					take := int64(n - off)
					if take > partRemain {
						take = partRemain
					}
					part.Write(buf[off : off+int(take)])
					partRemain -= take
					off += int(take)
					if partRemain == 0 {
						parts = append(parts, strings.ToUpper(hex.EncodeToString(part.Sum(nil))))
						part = md5.New()
						partRemain = sliceSize
					}
				}
			}
			done += int64(n)
			if report != nil {
				report(done, size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", "", rerr
		}
	}

	if done != size {
		return "", "", fmt.Errorf("读取字节数与文件大小不一致（期望 %d，实际 %d）", size, done)
	}

	full := strings.ToUpper(hex.EncodeToString(whole.Sum(nil)))
	if small {
		return full, full, nil
	}
	// 最后一片不足 sliceSize 也要计入
	if partRemain != sliceSize {
		parts = append(parts, strings.ToUpper(hex.EncodeToString(part.Sum(nil))))
	}
	if len(parts) == 0 {
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
