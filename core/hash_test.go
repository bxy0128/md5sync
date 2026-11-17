package core

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// naive 用最直白的方式算一遍，作为交叉验证的参考实现。
func naive(data []byte) (string, string) {
	full := strings.ToUpper(hex.EncodeToString(md5Sum(data)))
	size := int64(len(data))
	if size <= SmallLimit {
		return full, full
	}
	ss := int(SliceSize(size))
	var parts []string
	for i := 0; i < len(data); i += ss {
		end := i + ss
		if end > len(data) {
			end = len(data)
		}
		// 每片用大写十六进制，与天翼驱动一致
		parts = append(parts, strings.ToUpper(hex.EncodeToString(md5Sum(data[i:end]))))
	}
	agg := md5Sum([]byte(strings.Join(parts, "\n")))
	return full, strings.ToUpper(hex.EncodeToString(agg))
}

func md5Sum(b []byte) []byte {
	h := md5.Sum(b)
	return h[:]
}

func TestSliceSize(t *testing.T) {
	cases := []struct {
		size int64
		want int64
	}{
		{0, 10 * MiB},
		{3 * MiB, 10 * MiB},
		{1000*10*MiB - 1, 10 * MiB},
		{1000 * 10 * MiB, 20 * MiB},
		{1000*20*MiB - 1, 20 * MiB},
		{1000 * 20 * MiB, 50 * MiB},
		{2000*50*MiB - 1, 50 * MiB},
		{2000 * 50 * MiB, 60 * MiB},
		{129 << 30, 70 * MiB},
	}
	for _, c := range cases {
		if got := SliceSize(c.size); got != c.want {
			t.Errorf("SliceSize(%d) = %d, want %d", c.size, got, c.want)
		}
	}
}

func TestHashReaderMatchesNaive(t *testing.T) {
	sizes := []int64{
		0,
		31,
		3 * MiB,
		SmallLimit,
		SmallLimit + 1,
		25 * MiB,
		20 * MiB, // 恰好整除 10MiB
	}
	for _, size := range sizes {
		data := make([]byte, size)
		r := rand.New(rand.NewSource(size + 7))
		r.Read(data)

		wantMD5, wantSlice := naive(data)
		gotMD5, gotSlice, err := HashReader(bytes.NewReader(data), size, nil)
		if err != nil {
			t.Fatalf("size=%d: %v", size, err)
		}
		if gotMD5 != wantMD5 || gotSlice != wantSlice {
			t.Errorf("size=%d: got %s/%s want %s/%s", size, gotMD5, gotSlice, wantMD5, wantSlice)
		}
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "测试 文件.mkv")
	data := make([]byte, 12*MiB)
	rand.New(rand.NewSource(42)).Read(data)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	wantMD5, wantSlice := naive(data)
	got, err := HashFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MD5 != wantMD5 || got.SliceMD5 != wantSlice {
		t.Errorf("got %s/%s want %s/%s", got.MD5, got.SliceMD5, wantMD5, wantSlice)
	}
	if got.Name != "测试 文件.mkv" || got.Cloud != "189" || got.Size != int64(len(data)) {
		t.Errorf("字段异常: %+v", got)
	}
}

func TestProgressMonotonic(t *testing.T) {
	data := make([]byte, 5*MiB)
	rand.New(rand.NewSource(1)).Read(data)
	var last int64
	_, _, err := HashReader(bytes.NewReader(data), int64(len(data)), func(done, total int64) {
		if done < last || done > total {
			t.Fatalf("进度异常 done=%d last=%d total=%d", done, last, total)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != int64(len(data)) {
		t.Errorf("进度未到达末尾: %d", last)
	}
}
