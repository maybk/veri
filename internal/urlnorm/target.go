package urlnorm

import (
	"net/netip"
	"net/url"
	"strings"
)

var (
	cgnatPrefix  = netip.MustParsePrefix("100.64.0.0/10")
	fakeIPPrefix = netip.MustParsePrefix("198.18.0.0/15")
)

var localHostSuffixes = []string{
	".localhost",
	".local",
	".internal",
	".lan",
	".home.arpa",
	".intranet",
	".corp",
	".localdomain",
}

var chineseDomainSuffixes = []string{
	"zhihu.com",
	"qq.com",
	"163.com",
	"126.com",
	"sina.com",
	"sohu.com",
	"baidu.com",
	"bilibili.com",
	"csdn.net",
	"cnblogs.com",
	"oschina.net",
	"gitee.com",
	"yuque.com",
	"toutiao.com",
	"xiaohongshu.com",
	"douban.com",
	"aliyun.com",
	"alibabacloud.com",
	"tencent.com",
	"volcengine.com",
	"huaweicloud.com",
	"sspai.com",
	"segmentfault.com",
}

// IsLocalOrPrivateTarget 纯静态判断目标 URL 是否指向本机回环、私网/隧道网段、单标签主机或内网保留域名后缀。
// 命中此类目标时严禁向外部云端提取源转发（防止泄露内网拓扑与无效公网请求）。
func IsLocalOrPrivateTarget(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(u.Hostname()), "."))
	if host == "" {
		return false
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return true
		}
		unmapped := ip.Unmap()
		if unmapped.IsLoopback() ||
			unmapped.IsPrivate() ||
			unmapped.IsLinkLocalUnicast() ||
			unmapped.IsLinkLocalMulticast() ||
			unmapped.IsMulticast() ||
			unmapped.IsUnspecified() {
			return true
		}
		if unmapped.Is4() {
			b := unmapped.As4()
			if b[0] == 0 || b[0] >= 240 {
				return true
			}
			if cgnatPrefix.Contains(unmapped) || fakeIPPrefix.Contains(unmapped) {
				return true
			}
		}
		return false
	}

	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suf := range localHostSuffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// LooksLikePDFURL 判断目标 URL 路径是否明确指向 PDF 文档端点（供路由层优先调度支持 PDF 的云端提取源）。
func LooksLikePDFURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	cleanPath := strings.ToLower(strings.TrimRight(u.EscapedPath(), "/"))
	if strings.HasSuffix(cleanPath, ".pdf") {
		return true
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if (host == "arxiv.org" || strings.HasSuffix(host, ".arxiv.org")) && strings.HasPrefix(cleanPath, "/pdf/") {
		return true
	}
	if host == "openreview.net" && cleanPath == "/pdf" {
		return true
	}
	return false
}

// IsLikelyChineseHost 判断目标主机是否属于中国大陆常见域名体系（.cn 或主流中文内容平台），辅助提取链地域选路。
func IsLikelyChineseHost(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(u.Hostname()), "."))
	if host == "" {
		return false
	}
	if host == "cn" || strings.HasSuffix(host, ".cn") {
		return true
	}
	for _, dom := range chineseDomainSuffixes {
		if host == dom || strings.HasSuffix(host, "."+dom) {
			return true
		}
	}
	return false
}
