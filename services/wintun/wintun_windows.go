//go:build windows

package wintun

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const wintunURL = "https://www.wintun.net/builds/wintun-0.14.1.zip"
const wintunArchiveSHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
const maxWintunArchiveSize = 8 << 20
const maxWintunDLLSize = 2 << 20

var wintunDLLSHA256 = map[string]string{
	"amd64": "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce",
	"arm":   "daad267411ecdc70a0535e274d2c3e9da3d0084bdac7662cb8424dd4a031b4d9",
	"arm64": "f7ba89005544be9d85231a9e0d5f23b2d15b3311667e2dad0debd344918a3f80",
	"x86":   "d694fa46ab4cfebcb2632d094c7aa97278eef2f8052438621766d863ae98a931",
}

var wintunLoadMu sync.Mutex
var wintunLoaded bool

// Prepare 按需下载并预加载 Wintun，供 go-ios 使用。
func Prepare(dataDir string, client *http.Client, logInfo func(string)) error {
	wintunLoadMu.Lock()
	defer wintunLoadMu.Unlock()
	if wintunLoaded {
		return nil
	}
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86"
	}
	expected, ok := wintunDLLSHA256[arch]
	if !ok {
		return fmt.Errorf("不支持的 Wintun 架构: %s", runtime.GOARCH)
	}
	dir := filepath.Join(dataDir, arch)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建 Wintun 目录失败: %w", err)
	}
	path := filepath.Join(dir, "wintun.dll")
	valid, err := validWintunCache(path, expected)
	if err != nil {
		return err
	}
	if !valid {
		logInfo("正在从官网下载 Wintun")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		dll, err := downloadWintun(ctx, client, arch, expected)
		if err != nil {
			return err
		}
		if err := saveWintun(path, dll); err != nil {
			return fmt.Errorf("保存 Wintun 失败: %w", err)
		}
	}
	if _, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32); err != nil {
		return fmt.Errorf("加载 Wintun 失败: %w", err)
	}
	logInfo("已加载 Wintun: " + path)
	wintunLoaded = true
	return nil
}

func validWintunCache(path, expected string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("检查 Wintun 缓存失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("Wintun 缓存不是普通文件: %s", path)
	}
	if info.Size() <= 0 || info.Size() > maxWintunDLLSize {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("读取 Wintun 缓存失败: %w", err)
	}
	return wintunHashMatches(data, expected), nil
}

func wintunHashMatches(data []byte, expected string) bool {
	return fmt.Sprintf("%x", sha256.Sum256(data)) == expected
}

func downloadWintun(ctx context.Context, client *http.Client, arch, expected string) ([]byte, error) {
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return fmt.Errorf("Wintun 下载重定向无效")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wintunURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Wintun 下载请求失败: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 Wintun 失败，请检查网络或代理后重试: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 Wintun 失败，HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWintunArchiveSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取 Wintun 下载内容失败: %w", err)
	}
	if len(data) > maxWintunArchiveSize || !wintunHashMatches(data, wintunArchiveSHA256) {
		return nil, fmt.Errorf("Wintun 下载包大小或 SHA-256 校验失败")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解析 Wintun 下载包失败: %w", err)
	}
	name := "wintun/bin/" + arch + "/wintun.dll"
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		if !file.Mode().IsRegular() || file.UncompressedSize64 > maxWintunDLLSize {
			return nil, fmt.Errorf("Wintun DLL 类型或大小无效")
		}
		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("打开 Wintun DLL 失败: %w", err)
		}
		dll, readErr := io.ReadAll(io.LimitReader(reader, maxWintunDLLSize+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 Wintun DLL 失败: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭 Wintun DLL 失败: %w", closeErr)
		}
		if len(dll) > maxWintunDLLSize || !wintunHashMatches(dll, expected) {
			return nil, fmt.Errorf("Wintun DLL SHA-256 校验失败")
		}
		return dll, nil
	}
	return nil, fmt.Errorf("下载包中没有适用于 %s 的 Wintun DLL", arch)
}

func saveWintun(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".wintun-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
