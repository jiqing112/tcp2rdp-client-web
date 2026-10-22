package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// ProbeResult is the answer of the "测试服务器" button: what answered, how fast,
// and which certificate it presented. The fingerprint is the value -pin wants,
// so the page can offer it with one click.
type ProbeResult struct {
	Host        string  `json:"host"`
	IP          string  `json:"ip"`
	Port        string  `json:"port"`
	RDP         bool    `json:"rdp"` // the peer answered like an RDP server
	TCPms       float64 `json:"tcpMs"`
	Negotiatems float64 `json:"negotiateMs"`
	TLSms       float64 `json:"tlsMs"`
	TLSVersion  string  `json:"tlsVersion"`
	Cipher      string  `json:"cipher"`
	CN          string  `json:"cn"`
	Fingerprint string  `json:"fingerprint"`
	Message     string  `json:"message"`
}

// rdpCR is the plaintext a real RDP client sends first: TPKT + X.224 Connection
// Request carrying an RDP negotiation request for "TLS and CredSSP". Sending the
// same bytes is what makes a probe indistinguishable from mstsc.
var rdpCR = []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00, 0x0b, 0x00, 0x00, 0x00}

// rdpCipherSuites mirrors what the tunnel's own client offers.
var rdpCipherSuites = []uint16{
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
}

// Probe talks to the server the way the client does — X.224 negotiation, then
// TLS — without authenticating, so it can answer "is this host alive, is it our
// server, and which certificate does it show" before any tunnel exists.
func Probe(raw string, timeout time.Duration) (ProbeResult, error) {
	host, port, ok := splitHostPort(normalizeServer(raw))
	if !ok {
		return ProbeResult{}, fmt.Errorf("服务器地址无效：%q，应形如 1.2.3.4:3389", raw)
	}
	res := ProbeResult{Host: host, Port: port}

	ipaddr, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return res, fmt.Errorf("无法解析 %s：%w", host, err)
	}
	res.IP = ipaddr.String()

	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return res, fmt.Errorf("连不上 %s：%w", net.JoinHostPort(host, port), err)
	}
	defer conn.Close()
	res.TCPms = ms(time.Since(start))
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// 1) The same plaintext a remote desktop client opens with.
	if _, err := conn.Write(rdpCR); err != nil {
		return res, fmt.Errorf("发送 RDP 协商请求失败：%w", err)
	}
	start = time.Now()
	cc, err := readTPKT(conn)
	if err != nil {
		return res, fmt.Errorf("端口开着，但没有回 RDP 协商响应（可能不是本服务端，或被防火墙挡住）：%w", err)
	}
	res.Negotiatems = ms(time.Since(start))
	if len(cc) < 11 || cc[0] != 0x0e || cc[1] != 0xd0 {
		return res, fmt.Errorf("对端不像 RDP 服务端：回包 %s", hex.EncodeToString(cc[:min(8, len(cc))]))
	}
	res.RDP = true

	// 2) TLS, exactly the version and suites a Windows client would use.
	tc := &tls.Config{
		InsecureSkipVerify: true, // the tunnel authenticates inside; here we only read the certificate
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
		CipherSuites:       rdpCipherSuites,
	}
	tlsConn := tls.Client(conn, tc)
	start = time.Now()
	if err := tlsConn.Handshake(); err != nil {
		return res, fmt.Errorf("RDP 协商正常，但 TLS 握手失败：%w", err)
	}
	res.TLSms = ms(time.Since(start))

	st := tlsConn.ConnectionState()
	res.TLSVersion = tlsVersionName(st.Version)
	res.Cipher = tls.CipherSuiteName(st.CipherSuite)
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		sum := sha256.Sum256(leaf.Raw)
		res.Fingerprint = hex.EncodeToString(sum[:])
		res.CN = leaf.Subject.CommonName
	}
	res.Message = probeMessage(res)
	return res, nil
}

func probeMessage(r ProbeResult) string {
	parts := []string{"RDP 握手正常"}
	if r.CN != "" {
		parts = append(parts, "证书 CN="+r.CN)
	}
	if r.TLSVersion != "" {
		parts = append(parts, r.TLSVersion)
	}
	return strings.Join(parts, " · ")
}

// readTPKT reads one TPKT frame: a 4-byte header with a big-endian length.
func readTPKT(conn net.Conn) ([]byte, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, err
	}
	if head[0] != 0x03 {
		return nil, errors.New("不是 TPKT 报文")
	}
	n := int(binary.BigEndian.Uint16(head[2:4]))
	if n < 4 || n > 4096 {
		return nil, fmt.Errorf("TPKT 长度异常：%d", n)
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return body, nil
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()/100) / 10 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
