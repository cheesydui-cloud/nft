package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nft/internal/wsproto"
)

// handleCoreInstall downloads a proxy core from the panel and installs it under
// /var/lib/nft/cores/{type}/. Same HTTP-pull pattern as agent self-upgrade.
func handleCoreInstall(req wsproto.CoreInstall) wsproto.CoreInstallAck {
	typ := normalizeInstallCoreType(req.Type)
	if typ == "" {
		return wsproto.CoreInstallAck{OK: false, Error: "未知核心类型"}
	}
	if strings.TrimSpace(req.DownloadAt) == "" {
		return wsproto.CoreInstallAck{OK: false, Error: "缺少 download_at"}
	}
	if strings.TrimSpace(req.SHA256) == "" {
		return wsproto.CoreInstallAck{OK: false, Error: "缺少 sha256"}
	}

	destDir, destName := coreInstallPaths(typ)
	destPath := filepath.Join(destDir, destName)

	// Skip re-download if already present with matching sha.
	if data, err := os.ReadFile(destPath); err == nil {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) == strings.ToLower(req.SHA256) ||
			hex.EncodeToString(sum[:]) == req.SHA256 {
			log.Printf("core_install: %s already at %s (sha match)", typ, destPath)
			return wsproto.CoreInstallAck{OK: true, Version: req.Version, Path: destPath}
		}
	}

	binary, err := downloadCoreBinary(req)
	if err != nil {
		return wsproto.CoreInstallAck{OK: false, Error: err.Error()}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return wsproto.CoreInstallAck{OK: false, Error: "创建目录失败: " + err.Error()}
	}
	if err := atomicWriteExec(destPath, binary); err != nil {
		return wsproto.CoreInstallAck{OK: false, Error: "安装失败: " + err.Error()}
	}
	log.Printf("core_install: installed %s %s -> %s (%d bytes)", typ, req.Version, destPath, len(binary))
	return wsproto.CoreInstallAck{OK: true, Version: req.Version, Path: destPath}
}

func normalizeInstallCoreType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "xray":
		return "xray"
	case "sing-box", "singbox":
		return "sing-box"
	case "mita", "mieru", "mbox":
		return "mita"
	default:
		return ""
	}
}

// coreInstallPaths returns install dir and binary filename for a core type.
// Layout matches detectProxyCores / deploy* search paths.
func coreInstallPaths(typ string) (dir, name string) {
	base := coreStateDir()
	switch typ {
	case "xray":
		return filepath.Join(base, "xray"), "xray"
	case "sing-box":
		return filepath.Join(base, "sing-box"), "sing-box"
	case "mita":
		// detect uses name "mieru" with path .../mieru/mita
		return filepath.Join(base, "mieru"), "mita"
	default:
		return filepath.Join(base, typ), typ
	}
}

func downloadCoreBinary(req wsproto.CoreInstall) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	url := fixUpgradeDownloadURL(req.DownloadAt)
	client := &http.Client{}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("User-Agent", "nft-agent-core-install")
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	limit := req.Size + 1024
	if limit < 1 {
		limit = 120 << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(strings.TrimSpace(req.SHA256))
	if got != want && got != req.SHA256 {
		return nil, fmt.Errorf("sha256 mismatch: got %s, want %s", got, req.SHA256)
	}
	return data, nil
}

func atomicWriteExec(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nft-core-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
