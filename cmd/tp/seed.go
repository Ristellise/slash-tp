package main

import (
	cryptorand "crypto/rand"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"github.com/pion/webrtc/v4"
)

const seedLen = 16

type session struct {
	Seed  []byte
	Name  string
	Ufrag string
	Pwd   string
	Fp    string
	Cert  *webrtc.Certificate
}

func newSession() (*session, error) {
	seed := make([]byte, seedLen)
	if _, err := cryptorand.Read(seed); err != nil {
		return nil, err
	}
	return resumeSession(seed)
}

func resumeSession(seed []byte) (*session, error) {
	if len(seed) != seedLen {
		return nil, fmt.Errorf("bad seed length %d", len(seed))
	}
	cert, err := dtlsCert(seed)
	if err != nil {
		return nil, err
	}
	fps, err := cert.GetFingerprints()
	if err != nil {
		return nil, err
	}
	s := &session{
		Seed:  seed,
		Name:  sessionName(seed),
		Ufrag: base64.StdEncoding.EncodeToString(derive(seed, "ice-ufrag", 6)),
		Pwd:   base64.StdEncoding.EncodeToString(derive(seed, "ice-pwd", 18)),
		Fp:    fps[0].Value,
		Cert:  cert,
	}
	return s, nil
}

func (s *session) newPeerConnection(external string) (*webrtc.PeerConnection, error) {
	se := webrtc.SettingEngine{}
	se.SetICECredentials(s.Ufrag, s.Pwd)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.LoggerFactory = loggerFactory{}
	cfg := webrtc.Configuration{
		ICEServers:   []webrtc.ICEServer{{URLs: stunServers}},
		Certificates: []webrtc.Certificate{*s.Cert},
	}
	if external != "" {
		host, portS, err := net.SplitHostPort(external)
		if err != nil {
			return nil, fmt.Errorf("--public: want ip:port, got %q", external)
		}
		port, err := strconv.Atoi(portS)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("--public: bad port %q", portS)
		}
		if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("--public: want ipv4, got %q", host)
		}
		se.SetNAT1To1IPs([]string{host}, webrtc.ICECandidateTypeHost)
		se.SetEphemeralUDPPortRange(uint16(port), uint16(port))
		cfg.ICEServers = nil
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	return api.NewPeerConnection(cfg)
}

func derive(seed []byte, label string, n int) []byte {
	buf := make([]byte, 0, len(label)+len(seed))
	buf = append(buf, label...)
	buf = append(buf, seed...)
	out := make([]byte, 0, n+sha256.Size)
	for len(out) < n {
		h := sha256.Sum256(buf)
		out = append(out, h[:]...)
		buf = h[:]
	}
	return out[:n]
}

func sessionName(seed []byte) string {
	a := binary.LittleEndian.Uint64(derive(seed, "name-a", 8))
	b := binary.LittleEndian.Uint64(derive(seed, "name-b", 8))
	return generateSessionName(rand.New(rand.NewPCG(a, b)))
}

func dtlsCert(seed []byte) (*webrtc.Certificate, error) {
	key := ed25519.NewKeyFromSeed(derive(seed, "dtls-key", 32))
	tpl := x509.Certificate{
		Issuer:       pkix.Name{CommonName: "tp"},
		NotBefore:    time.Unix(0, 0).UTC(),
		NotAfter:     time.Unix(4102444800, 0).UTC(),
		SerialNumber: big.NewInt(1),
		Version:      2,
		Subject:      pkix.Name{CommonName: "tp"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(cryptorand.Reader, &tpl, &tpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	c := webrtc.CertificateFromX509(key, cert)
	return &c, nil
}
