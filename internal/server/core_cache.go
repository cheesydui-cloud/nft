package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Panel-side cache of proxy core binaries (xray / sing-box / mita), keyed by
// type+arch. Agents pull via GET /v1/cores/{type}?arch=… after a core_install
// WS frame (same HTTP-not-WS pattern as agent upgrade).
const coresCacheRoot = "/var/lib/nft/cores-cache"

var (
	coreCacheMu sync.Mutex
)

// coreMeta is written next to each cached binary.
type coreMeta struct {
	Type      string `json:"type"`
	Arch      string `json:"arch"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	SourceURL string `json:"source_url,omitempty"`
	FetchedAt int64  `json:"fetched_at"`
}

// CoreCacheEntry is the admin list item.
type CoreCacheEntry struct {
	Type      string `json:"type"`
	Arch      string `json:"arch"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	SourceURL string `json:"source_url,omitempty"`
	FetchedAt int64  `json:"fetched_at"`
	Path      string `json:"path,omitempty"`
}

func coreCacheDir(coreType, arch string) string {
	return filepath.Join(coresCacheRoot, sanitizeCoreType(coreType), sanitizeArch(arch))
}

func coreBinaryName(coreType string) string {
	switch sanitizeCoreType(coreType) {
	case "xray":
		return "xray"
	case "sing-box":
		return "sing-box"
	case "mita", "mieru":
		return "mita"
	default:
		return sanitizeCoreType(coreType)
	}
}

// normalizeCoreType maps UI/protocol aliases onto cache keys.
// mieru protocol uses mita server binary; cache key is "mita".
func normalizeCoreType(coreType string) string {
	t := strings.ToLower(strings.TrimSpace(coreType))
	switch t {
	case "mieru", "mbox":
		return "mita"
	case "singbox":
		return "sing-box"
	default:
		return t
	}
}

func sanitizeCoreType(coreType string) string {
	t := normalizeCoreType(coreType)
	switch t {
	case "xray", "sing-box", "mita":
		return t
	default:
		// keep alnum + dash only
		var b strings.Builder
		for _, r := range t {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
}

func sanitizeArch(arch string) string {
	a := strings.ToLower(strings.TrimSpace(arch))
	switch a {
	case "x86_64", "x64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return a
	}
}

func listCoreCache() ([]CoreCacheEntry, error) {
	root := coresCacheRoot
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []CoreCacheEntry{}, nil
		}
		return nil, err
	}
	var out []CoreCacheEntry
	for _, typ := range []string{"xray", "sing-box", "mita"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if e, err := readCoreMeta(typ, arch); err == nil && e != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func readCoreMeta(coreType, arch string) (*CoreCacheEntry, error) {
	dir := coreCacheDir(coreType, arch)
	metaPath := filepath.Join(dir, "meta.json")
	binPath := filepath.Join(dir, coreBinaryName(coreType))
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var m coreMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if st, err := os.Stat(binPath); err != nil || st.IsDir() {
		return nil, fmt.Errorf("binary missing for %s/%s", coreType, arch)
	}
	return &CoreCacheEntry{
		Type:      m.Type,
		Arch:      m.Arch,
		Version:   m.Version,
		SHA256:    m.SHA256,
		Size:      m.Size,
		SourceURL: m.SourceURL,
		FetchedAt: m.FetchedAt,
		Path:      binPath,
	}, nil
}

func loadCoreBinary(coreType, arch string) (data []byte, meta *CoreCacheEntry, err error) {
	e, err := readCoreMeta(coreType, arch)
	if err != nil {
		return nil, nil, err
	}
	data, err = os.ReadFile(e.Path)
	if err != nil {
		return nil, nil, err
	}
	return data, e, nil
}

func deleteCoreCache(coreType, arch string) error {
	dir := coreCacheDir(coreType, arch)
	return os.RemoveAll(dir)
}

// ghProxyPrefix returns an optional URL prefix (e.g. https://gh-proxy.com/)
// from /etc/nft/gh-proxy or NFTF_GH_PROXY, matching install.sh.
func ghProxyPrefix() string {
	if v := strings.TrimSpace(os.Getenv("NFTF_GH_PROXY")); v != "" {
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		return v
	}
	b, err := os.ReadFile("/etc/nft/gh-proxy")
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return ""
	}
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v
}

func withGHProxy(url string) string {
	pfx := ghProxyPrefix()
	if pfx == "" {
		return url
	}
	// Already proxied?
	if strings.HasPrefix(url, pfx) {
		return url
	}
	return pfx + url
}

func httpGetBytesLong(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nft-panel-core-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	// Cap at 200MB for safety.
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func fetchGitHubRelease(repo, version string) (*ghRelease, error) {
	// "latest" must mean highest semver tag, not GitHub's sticky /releases/latest
	// (which follows published_at; a re-touch of an older tag can pin latest wrongly).
	if version == "" || version == "latest" {
		return fetchGitHubLatestSemverRelease(repo)
	}
	tag := version
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	body, err := httpGetBytesLong(withGHProxy(apiURL), 60*time.Second)
	if err != nil {
		if pfx := ghProxyPrefix(); pfx != "" {
			body, err = httpGetBytesLong(apiURL, 60*time.Second)
		}
		if err != nil {
			return nil, err
		}
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parse github release: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("github release empty for %s %s", repo, version)
	}
	return &rel, nil
}

// fetchGitHubLatestSemverRelease lists recent releases and returns the highest
// non-draft, non-prerelease semver tag. Falls back to /releases/latest on error.
func fetchGitHubLatestSemverRelease(repo string) (*ghRelease, error) {
	listURL := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", repo)
	body, err := httpGetBytesLong(withGHProxy(listURL), 60*time.Second)
	if err != nil {
		if pfx := ghProxyPrefix(); pfx != "" {
			body, err = httpGetBytesLong(listURL, 60*time.Second)
		}
	}
	if err == nil {
		var list []struct {
			TagName    string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
			Assets     []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if jerr := json.Unmarshal(body, &list); jerr == nil && len(list) > 0 {
			var best *ghRelease
			bestTag := ""
			for i := range list {
				item := list[i]
				if item.Draft || item.Prerelease {
					continue
				}
				tag := strings.TrimSpace(item.TagName)
				if tag == "" || parseSemver(tag) == nil {
					continue
				}
				// pick tag if none yet, or if tag is strictly newer than bestTag
				if bestTag == "" || (tag != bestTag && !semverGE(bestTag, tag)) {
					bestTag = tag
					rel := &ghRelease{TagName: tag}
					for _, a := range item.Assets {
						rel.Assets = append(rel.Assets, struct {
							Name               string `json:"name"`
							BrowserDownloadURL string `json:"browser_download_url"`
						}{Name: a.Name, BrowserDownloadURL: a.BrowserDownloadURL})
					}
					best = rel
				}
			}
			if best != nil {
				return best, nil
			}
		}
	}
	// Fallback: GitHub's /releases/latest
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	body, err = httpGetBytesLong(withGHProxy(apiURL), 60*time.Second)
	if err != nil {
		if pfx := ghProxyPrefix(); pfx != "" {
			body, err = httpGetBytesLong(apiURL, 60*time.Second)
		}
		if err != nil {
			return nil, err
		}
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parse github release: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("github release empty for %s latest", repo)
	}
	return &rel, nil
}

func pickAsset(rel *ghRelease, wantNames ...string) (name, url string, err error) {
	byName := map[string]string{}
	for _, a := range rel.Assets {
		byName[a.Name] = a.BrowserDownloadURL
	}
	for _, n := range wantNames {
		if u, ok := byName[n]; ok {
			return n, u, nil
		}
	}
	// substring fallback
	for _, want := range wantNames {
		for n, u := range byName {
			if strings.Contains(n, want) {
				return n, u, nil
			}
		}
	}
	return "", "", fmt.Errorf("no matching asset in %s (want %v)", rel.TagName, wantNames)
}

// assetCandidates returns preferred GitHub asset names for type+arch.
func assetCandidates(coreType, arch, version string) (repo string, names []string) {
	typ := sanitizeCoreType(coreType)
	arch = sanitizeArch(arch)
	ver := strings.TrimPrefix(version, "v")
	switch typ {
	case "xray":
		repo = "XTLS/Xray-core"
		if arch == "arm64" {
			names = []string{"Xray-linux-arm64-v8a.zip"}
		} else {
			names = []string{"Xray-linux-64.zip"}
		}
	case "sing-box":
		repo = "SagerNet/sing-box"
		// Prefer plain tar.gz (not glibc/musl variants).
		if arch == "arm64" {
			names = []string{
				fmt.Sprintf("sing-box-%s-linux-arm64.tar.gz", ver),
				"sing-box-", // fallback match
			}
		} else {
			names = []string{
				fmt.Sprintf("sing-box-%s-linux-amd64.tar.gz", ver),
				"sing-box-",
			}
		}
	case "mita":
		repo = "enfein/mieru"
		if arch == "arm64" {
			names = []string{
				fmt.Sprintf("mita_%s_linux_arm64.tar.gz", ver),
				"mita_",
			}
		} else {
			names = []string{
				fmt.Sprintf("mita_%s_linux_amd64.tar.gz", ver),
				"mita_",
			}
		}
	}
	return repo, names
}

// extractCoreBinary pulls the named binary out of a zip/tar.gz/raw payload.
func extractCoreBinary(coreType string, archive []byte, assetName string) ([]byte, error) {
	want := coreBinaryName(coreType)
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractFromZip(archive, want)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractFromTarGz(archive, want)
	default:
		// raw binary
		if len(archive) < 16 {
			return nil, fmt.Errorf("payload too small")
		}
		return archive, nil
	}
}

func extractFromZip(data []byte, wantName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var fallback []byte
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, 120<<20))
		rc.Close()
		if err != nil {
			continue
		}
		if base == wantName {
			return b, nil
		}
		// Prefer executable-looking files named closely.
		if fallback == nil && (base == wantName || strings.Contains(base, wantName)) {
			fallback = b
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("zip 中未找到二进制 %s", wantName)
}

func extractFromTarGz(data []byte, wantName string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var fallback []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		b, err := io.ReadAll(io.LimitReader(tr, 120<<20))
		if err != nil {
			continue
		}
		if base == wantName {
			return b, nil
		}
		if fallback == nil && strings.Contains(base, wantName) {
			fallback = b
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("tar 中未找到二进制 %s", wantName)
}

func writeCoreCache(coreType, arch, version, sourceURL string, binary []byte) (*CoreCacheEntry, error) {
	coreType = sanitizeCoreType(coreType)
	arch = sanitizeArch(arch)
	if coreType == "" || arch == "" {
		return nil, fmt.Errorf("type/arch required")
	}
	if len(binary) < 16 {
		return nil, fmt.Errorf("binary too small")
	}
	sum := sha256.Sum256(binary)
	sha := hex.EncodeToString(sum[:])
	dir := coreCacheDir(coreType, arch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	binPath := filepath.Join(dir, coreBinaryName(coreType))
	tmp := binPath + ".tmp"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, binPath); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	meta := coreMeta{
		Type:      coreType,
		Arch:      arch,
		Version:   version,
		SHA256:    sha,
		Size:      int64(len(binary)),
		SourceURL: sourceURL,
		FetchedAt: time.Now().Unix(),
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), mb, 0o644); err != nil {
		return nil, err
	}
	return &CoreCacheEntry{
		Type: meta.Type, Arch: meta.Arch, Version: meta.Version,
		SHA256: meta.SHA256, Size: meta.Size, SourceURL: meta.SourceURL,
		FetchedAt: meta.FetchedAt, Path: binPath,
	}, nil
}

// fetchCoreToCache downloads one type+arch into the panel cache.
// version empty = latest. customURL if set skips GitHub asset resolution
// (still extracts zip/tar if URL ends with those suffixes).
func fetchCoreToCache(coreType, arch, version, customURL, wantSHA string) (*CoreCacheEntry, error) {
	coreCacheMu.Lock()
	defer coreCacheMu.Unlock()

	coreType = sanitizeCoreType(coreType)
	arch = sanitizeArch(arch)
	if coreType != "xray" && coreType != "sing-box" && coreType != "mita" {
		return nil, fmt.Errorf("不支持的核心类型: %s（支持 xray / sing-box / mita）", coreType)
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("不支持的架构: %s（支持 amd64 / arm64）", arch)
	}

	var (
		srcURL    string
		assetName string
		ver       = strings.TrimSpace(version)
	)

	if strings.TrimSpace(customURL) != "" {
		srcURL = strings.TrimSpace(customURL)
		assetName = filepath.Base(strings.Split(srcURL, "?")[0])
	} else {
		// Resolve version via GitHub API first so asset names can use it.
		repo, _ := assetCandidates(coreType, arch, ver)
		rel, err := fetchGitHubRelease(repo, ver)
		if err != nil {
			return nil, fmt.Errorf("查询 GitHub release 失败: %w", err)
		}
		ver = rel.TagName
		_, names := assetCandidates(coreType, arch, ver)
		// For sing-box/mita fallback names that are prefixes, filter assets carefully.
		name, url, err := pickAssetFiltered(rel, coreType, arch, names)
		if err != nil {
			return nil, err
		}
		assetName, srcURL = name, url
	}

	dlURL := withGHProxy(srcURL)
	data, err := httpGetBytesLong(dlURL, 5*time.Minute)
	if err != nil && ghProxyPrefix() != "" && strings.HasPrefix(dlURL, ghProxyPrefix()) {
		// retry direct
		data, err = httpGetBytesLong(srcURL, 5*time.Minute)
	}
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}

	binary, err := extractCoreBinary(coreType, data, assetName)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	if wantSHA != "" {
		sum := sha256.Sum256(binary)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, wantSHA) {
			return nil, fmt.Errorf("sha256 校验失败: got %s want %s", got, wantSHA)
		}
	}
	if ver == "" {
		ver = "unknown"
	}
	return writeCoreCache(coreType, arch, ver, srcURL, binary)
}

func pickAssetFiltered(rel *ghRelease, coreType, arch string, names []string) (string, string, error) {
	arch = sanitizeArch(arch)
	// First try exact preferred names.
	for _, n := range names {
		if !strings.Contains(n, "-") && !strings.HasSuffix(n, ".zip") && !strings.HasSuffix(n, ".tar.gz") {
			continue // skip bare prefix placeholders in first pass
		}
		for _, a := range rel.Assets {
			if a.Name == n {
				return a.Name, a.BrowserDownloadURL, nil
			}
		}
	}
	// Heuristic by type+arch.
	var prefer []string
	switch sanitizeCoreType(coreType) {
	case "xray":
		if arch == "arm64" {
			prefer = []string{"linux-arm64", "arm64-v8a"}
		} else {
			prefer = []string{"linux-64", "linux-amd64"}
		}
	case "sing-box":
		if arch == "arm64" {
			prefer = []string{"linux-arm64.tar.gz"}
		} else {
			prefer = []string{"linux-amd64.tar.gz"}
		}
	case "mita":
		if arch == "arm64" {
			prefer = []string{"linux_arm64.tar.gz"}
		} else {
			prefer = []string{"linux_amd64.tar.gz"}
		}
	}
	for _, a := range rel.Assets {
		ln := strings.ToLower(a.Name)
		// skip checksums / packages we don't unpack
		if strings.Contains(ln, "sha256") || strings.HasSuffix(ln, ".rpm") || strings.HasSuffix(ln, ".deb") {
			continue
		}
		if strings.Contains(ln, "android") || strings.Contains(ln, "macos") || strings.Contains(ln, "windows") {
			continue
		}
		if strings.Contains(ln, "glibc") || strings.Contains(ln, "musl") {
			continue
		}
		for _, p := range prefer {
			if strings.Contains(ln, p) {
				return a.Name, a.BrowserDownloadURL, nil
			}
		}
	}
	return "", "", fmt.Errorf("release %s 无匹配的 %s/%s 资产", rel.TagName, coreType, arch)
}

// --- HTTP handlers ---

func coreGitHubRepo(coreType string) string {
	switch sanitizeCoreType(coreType) {
	case "xray":
		return "XTLS/Xray-core"
	case "sing-box":
		return "SagerNet/sing-box"
	case "mita":
		return "enfein/mieru"
	default:
		return ""
	}
}

func isKnownCoreType(t string) bool {
	switch sanitizeCoreType(t) {
	case "xray", "sing-box", "mita":
		return true
	default:
		return false
	}
}

// coreVersionBehind reports whether cached is older / different from latest.
func coreVersionBehind(cached, latest string) bool {
	c := strings.TrimSpace(cached)
	l := strings.TrimSpace(latest)
	if l == "" {
		return false
	}
	if c == "" {
		return true
	}
	if c == l {
		return false
	}
	// same tag ignoring leading v
	if strings.TrimPrefix(c, "v") == strings.TrimPrefix(l, "v") {
		return false
	}
	// cached already >= latest → not behind
	if parseSemver(c) != nil && parseSemver(l) != nil && semverGE(c, l) {
		return false
	}
	return true
}

// apiCheckProxyCores compares cached versions against GitHub latest (semver).
// GET /api/proxy-cores/check
func (s *Server) apiCheckProxyCores(w http.ResponseWriter, r *http.Request) {
	types := []string{"xray", "sing-box", "mita"}
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		t := sanitizeCoreType(raw)
		if !isKnownCoreType(t) {
			jsonErr(w, http.StatusBadRequest, "type 须为 xray / sing-box / mita")
			return
		}
		types = []string{t}
	}
	var items []map[string]any
	anyUpdate := false
	for _, typ := range types {
		repo := coreGitHubRepo(typ)
		cachedList := []map[string]any{}
		item := map[string]any{
			"type":             typ,
			"repo":             repo,
			"cached":           cachedList,
			"latest_version":   "",
			"update_available": false,
			"error":            "",
		}
		for _, arch := range []string{"amd64", "arm64"} {
			if e, err := readCoreMeta(typ, arch); err == nil && e != nil {
				cachedList = append(cachedList, map[string]any{
					"arch": e.Arch, "version": e.Version, "size": e.Size, "fetched_at": e.FetchedAt,
				})
			}
		}
		item["cached"] = cachedList
		if repo == "" {
			item["error"] = "unknown type"
			items = append(items, item)
			continue
		}
		rel, err := fetchGitHubLatestSemverRelease(repo)
		if err != nil {
			item["error"] = err.Error()
			items = append(items, item)
			continue
		}
		latest := strings.TrimSpace(rel.TagName)
		item["latest_version"] = latest
		need := false
		if len(cachedList) == 0 {
			need = latest != ""
		} else {
			if len(cachedList) < 2 {
				need = true // incomplete cache (want both arches for panel push)
			}
			for _, c := range cachedList {
				ver, _ := c["version"].(string)
				if coreVersionBehind(ver, latest) {
					need = true
					break
				}
			}
		}
		item["update_available"] = need
		if need {
			anyUpdate = true
		}
		items = append(items, item)
	}
	list, _ := listCoreCache()
	jsonOK(w, map[string]any{
		"items":            items,
		"update_available": anyUpdate,
		"cores":            list,
	})
}

// apiUpgradeProxyCores downloads latest (or specified version) for one/all core types.
// POST /api/proxy-cores/upgrade  body: { "type"?: "all"|"xray"|"sing-box"|"mita", "arch"?: "" }
func (s *Server) apiUpgradeProxyCores(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type    string `json:"type"`
		Version string `json:"version"`
		Arch    string `json:"arch"`
	}
	if r.Body != nil {
		_ = decodeJSON(r, &body)
	}
	typIn := strings.ToLower(strings.TrimSpace(body.Type))
	var types []string
	switch typIn {
	case "", "all":
		types = []string{"xray", "sing-box", "mita"}
	default:
		t := sanitizeCoreType(typIn)
		if !isKnownCoreType(t) {
			jsonErr(w, http.StatusBadRequest, "type 须为 all / xray / sing-box / mita")
			return
		}
		types = []string{t}
	}
	arches := []string{"amd64", "arm64"}
	if a := sanitizeArch(body.Arch); a == "amd64" || a == "arm64" {
		arches = []string{a}
	}
	ver := strings.TrimSpace(body.Version) // empty = latest
	var results []map[string]any
	okN := 0
	var lastErr string
	for _, typ := range types {
		for _, arch := range arches {
			e, err := fetchCoreToCache(typ, arch, ver, "", "")
			if err != nil {
				lastErr = err.Error()
				results = append(results, map[string]any{"type": typ, "arch": arch, "ok": false, "error": err.Error()})
				continue
			}
			okN++
			results = append(results, map[string]any{
				"type": e.Type, "arch": e.Arch, "ok": true,
				"version": e.Version, "sha256": e.SHA256, "size": e.Size,
			})
		}
	}
	if okN == 0 {
		msg := lastErr
		if msg == "" {
			msg = "升级失败"
		}
		jsonErr(w, http.StatusBadGateway, msg)
		return
	}
	list, _ := listCoreCache()
	jsonOK(w, map[string]any{
		"ok":      true,
		"results": results,
		"cores":   list,
		"message": fmt.Sprintf("已更新 %d 个缓存项", okN),
	})
}

func (s *Server) apiListProxyCores(w http.ResponseWriter, r *http.Request) {
	list, err := listCoreCache()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []CoreCacheEntry{}
	}
	jsonOK(w, map[string]any{"cores": list})
}

type proxyCoreFetchBody struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	// Arch empty = both amd64 and arm64. Single arch when set.
	Arch string `json:"arch"`
}

func (s *Server) apiFetchProxyCores(w http.ResponseWriter, r *http.Request) {
	var body proxyCoreFetchBody
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	typ := sanitizeCoreType(body.Type)
	if typ == "" {
		jsonErr(w, http.StatusBadRequest, "请指定核心类型 type（xray / sing-box / mita）")
		return
	}
	arches := []string{"amd64", "arm64"}
	if a := sanitizeArch(body.Arch); a != "" {
		arches = []string{a}
	}
	// Custom URL only makes sense for a single arch.
	if strings.TrimSpace(body.URL) != "" && len(arches) > 1 {
		jsonErr(w, http.StatusBadRequest, "自定义 URL 时请指定 arch（amd64 或 arm64）")
		return
	}
	var results []map[string]any
	var lastErr string
	for _, arch := range arches {
		e, err := fetchCoreToCache(typ, arch, body.Version, body.URL, body.SHA256)
		if err != nil {
			lastErr = err.Error()
			results = append(results, map[string]any{"type": typ, "arch": arch, "ok": false, "error": err.Error()})
			continue
		}
		results = append(results, map[string]any{
			"type": e.Type, "arch": e.Arch, "ok": true,
			"version": e.Version, "sha256": e.SHA256, "size": e.Size,
		})
	}
	okN := 0
	for _, r := range results {
		if r["ok"] == true {
			okN++
		}
	}
	if okN == 0 {
		msg := lastErr
		if msg == "" {
			msg = "下载失败"
		}
		jsonErr(w, http.StatusBadGateway, msg)
		return
	}
	list, _ := listCoreCache()
	jsonOK(w, map[string]any{"results": results, "cores": list})
}

func (s *Server) apiDeleteProxyCore(w http.ResponseWriter, r *http.Request) {
	typ := sanitizeCoreType(chi.URLParam(r, "type"))
	arch := sanitizeArch(chi.URLParam(r, "arch"))
	if typ == "" || arch == "" {
		jsonErr(w, http.StatusBadRequest, "bad type/arch")
		return
	}
	if err := deleteCoreCache(typ, arch); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, _ := listCoreCache()
	jsonOK(w, map[string]any{"ok": true, "cores": list})
}

// serveCoreBinary is GET /v1/cores/{type}?arch=amd64 — agent download endpoint.
func (s *Server) serveCoreBinary(w http.ResponseWriter, r *http.Request) {
	typ := sanitizeCoreType(chi.URLParam(r, "type"))
	arch := sanitizeArch(r.URL.Query().Get("arch"))
	if arch == "" {
		arch = "amd64"
	}
	data, meta, err := loadCoreBinary(typ, arch)
	if err != nil {
		http.Error(w, "core not cached: "+err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("X-SHA256", meta.SHA256)
	w.Header().Set("X-Core-Version", meta.Version)
	w.Header().Set("X-Core-Type", meta.Type)
	w.Header().Set("X-Core-Arch", meta.Arch)
	_, _ = w.Write(data)
}

// coreNeededForProtocol maps proxy service protocol → cache core type.
func coreNeededForProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vless":
		return "xray"
	case "shadowsocks", "ss":
		return "sing-box"
	case "mieru":
		return "mita"
	case "socks5", "socks", "anytls", "naive", "naiveproxy":
		return "sing-box"
	default:
		return ""
	}
}
