package host

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// CertInfo는 인증서 하나의 요약입니다.
type CertInfo struct {
	Path     string // 파일 경로
	Label    string // 표시 이름 (kubeconfig 안의 인증서는 "admin.conf (client)" 등)
	Subject  string
	Issuer   string
	NotAfter time.Time
	IsCA     bool
}

// DaysLeft는 만료까지 남은 일수입니다.
func (c CertInfo) DaysLeft(now time.Time) int {
	return int(c.NotAfter.Sub(now).Hours() / 24)
}

// ParseCertFile은 PEM 파일의 첫 번째 인증서를 읽습니다.
func ParseCertFile(path string) (CertInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CertInfo{}, err
	}
	certs := parsePEM(data)
	if len(certs) == 0 {
		return CertInfo{}, fmt.Errorf("%s: PEM 인증서가 아닙니다", path)
	}
	return info(path, certs[0]), nil
}

func info(path string, c *x509.Certificate) CertInfo {
	return CertInfo{Path: path, Subject: c.Subject.CommonName, Issuer: c.Issuer.CommonName, NotAfter: c.NotAfter, IsCA: c.IsCA}
}

func parsePEM(data []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return out
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			out = append(out, c)
		}
	}
}

// kubeconfigCerts는 kubeconfig 파일에 들어 있는 client-certificate-data를 읽습니다.
func kubeconfigCerts(path string) []*x509.Certificate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var kc struct {
		Users []struct {
			User struct {
				Data string `json:"client-certificate-data"`
				File string `json:"client-certificate"`
			} `json:"user"`
		} `json:"users"`
	}
	if yaml.Unmarshal(data, &kc) != nil {
		return nil
	}
	var out []*x509.Certificate
	for _, u := range kc.Users {
		switch {
		case u.User.Data != "":
			raw, err := base64.StdEncoding.DecodeString(u.User.Data)
			if err == nil {
				out = append(out, parsePEM(raw)...)
			}
		case u.User.File != "":
			if raw, err := os.ReadFile(u.User.File); err == nil {
				out = append(out, parsePEM(raw)...)
			}
		}
	}
	return out
}

// CertSource는 인증서를 찾을 위치입니다.
type CertSource struct {
	Glob       string // 파일 패턴 (*.crt, *.pem)
	Kubeconfig bool   // true면 kubeconfig 안의 클라이언트 인증서를 읽습니다
}

// ScanCerts는 여러 위치의 인증서를 읽어 만료가 빠른 순서로 돌려줍니다.
// base 기준 상대 경로를 Label로 씁니다. 읽을 권한이 없으면 ErrNotRoot입니다.
func ScanCerts(base string, sources []CertSource) ([]CertInfo, error) {
	var out []CertInfo
	denied := false
	seen := map[string]bool{}
	for _, src := range sources {
		files, _ := filepath.Glob(src.Glob)
		for _, f := range files {
			if seen[f] {
				continue
			}
			seen[f] = true
			rel, err := filepath.Rel(base, f)
			if err != nil || strings.HasPrefix(rel, "..") {
				rel = f
			}
			if src.Kubeconfig {
				for _, c := range kubeconfigCerts(f) {
					ci := info(f, c)
					ci.Label = rel + " (client)"
					out = append(out, ci)
				}
				continue
			}
			ci, err := ParseCertFile(f)
			if err != nil {
				if os.IsPermission(err) {
					denied = true
				}
				continue
			}
			ci.Label = rel
			out = append(out, ci)
		}
	}
	if len(out) == 0 && denied {
		return nil, ErrNotRoot
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NotAfter.Before(out[j].NotAfter) })
	return out, nil
}

// DescribeCertFile은 PEM 파일 또는 kubeconfig 안의 인증서 상세를 사람이 읽을 수 있게 만듭니다.
func DescribeCertFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	certs := parsePEM(data)
	if len(certs) == 0 {
		certs = kubeconfigCerts(path)
	}
	if len(certs) == 0 {
		return "", fmt.Errorf("%s: 인증서를 찾을 수 없습니다", path)
	}
	var sb strings.Builder
	for i, cert := range certs {
		fmt.Fprintf(&sb, "── 인증서 #%d ──\n", i+1)
		fmt.Fprintf(&sb, "Subject:      %s\n", cert.Subject)
		fmt.Fprintf(&sb, "Issuer:       %s\n", cert.Issuer)
		fmt.Fprintf(&sb, "Serial:       %s\n", cert.SerialNumber)
		fmt.Fprintf(&sb, "Not Before:   %s\n", cert.NotBefore.Local())
		fmt.Fprintf(&sb, "Not After:    %s\n", cert.NotAfter.Local())
		fmt.Fprintf(&sb, "Is CA:        %t\n", cert.IsCA)
		if len(cert.DNSNames) > 0 {
			fmt.Fprintf(&sb, "DNS SANs:     %s\n", strings.Join(cert.DNSNames, ", "))
		}
		if len(cert.IPAddresses) > 0 {
			ips := make([]string, len(cert.IPAddresses))
			for j, ip := range cert.IPAddresses {
				ips[j] = ip.String()
			}
			fmt.Fprintf(&sb, "IP SANs:      %s\n", strings.Join(ips, ", "))
		}
		var usages []string
		for _, u := range cert.ExtKeyUsage {
			switch u {
			case x509.ExtKeyUsageServerAuth:
				usages = append(usages, "serverAuth")
			case x509.ExtKeyUsageClientAuth:
				usages = append(usages, "clientAuth")
			}
		}
		if len(usages) > 0 {
			fmt.Fprintf(&sb, "Ext Usage:    %s\n", strings.Join(usages, ", "))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}
